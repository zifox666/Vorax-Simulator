package application

import (
	"fmt"
	"testing"

	"vorax/internal/ai"
	pb "vorax/internal/protocol"
)

// playAI 用真实服务层打一整局：每回合 AIDecide 给观察、要动作，然后执行命令推进。
// AI 全程只看到 Observation（不含 seed/RNG）。
// 返回终局分、整局使用的药剂刷新次数与用具刷新次数（按决策时的候选类别区分）。
func playAI(t *testing.T, svc *Service, seed string, pet int32, strategy ai.Strategy, params ai.Params) (int64, int, int) {
	t.Helper()
	run, err := svc.Create(&pb.CreateRunRequest{UserId: "ai-test", RequestId: "ai-integration-0001", Seed: seed, PetRefreshes: pet})
	if err != nil {
		t.Fatal(err)
	}
	token, view := run.StateToken, run.View
	steps, potionRefreshesUsed, toolRefreshesUsed := 0, 0, 0
	for view.State.Phase != pb.Phase_FINISHED {
		act, obs, err := svc.AIDecide(token, strategy, params)
		if err != nil {
			t.Fatalf("策略 %s 决策失败: %v", strategy, err)
		}
		if obs == nil || obs.Done() {
			break
		}
		if act.Type == "refresh" {
			if obs.Offer.Kind == int32(pb.CardKind_POTION) {
				potionRefreshesUsed++
			} else if obs.Offer.Kind == int32(pb.CardKind_TOOL) {
				toolRefreshesUsed++
			}
		}
		cmd := &pb.Command{Type: act.Type, CardId: act.CardID, OfferId: view.State.Offer.Id}
		if act.Type == "choose" {
			for _, idx := range act.Slots {
				m := view.State.Slots[idx].Monster
				if m == nil {
					t.Fatalf("动作目标槽 %d 无怪物", idx)
				}
				cmd.TargetIds = append(cmd.TargetIds, m.Id)
			}
		}
		next, err := svc.Command(view.State.RunId, &pb.CommandRequest{
			StateToken: token, ExpectedRevision: view.State.Revision,
			RequestId: fmt.Sprintf("ai-integration-step-%d", steps), Command: cmd,
		})
		if err != nil {
			t.Fatalf("执行 AI 动作 %v 失败: %v", act, err)
		}
		token, view = next.StateToken, next.View
		steps++
		if steps > 64 {
			t.Fatal("回合数异常")
		}
	}
	return view.State.Score, potionRefreshesUsed, toolRefreshesUsed
}

// TestAIIntegration 端到端正确性：两种隐藏信息策略都能完整打完一局且分数为正。
// （注意：单局分数对比受采样方差影响，不做硬断言；统计对比见 TestAIBenchmark。）
func TestAIIntegration(t *testing.T) {
	svc := service(t)
	for _, seed := range []string{"ai-demo-1", "ai-demo-2"} {
		for _, pet := range []int32{0, 2} {
			g, _, _ := playAI(t, svc, seed, pet, ai.StrategyGreedy, ai.Params{Samples: 8})
			s, _, _ := playAI(t, svc, seed, pet, ai.StrategySampler, ai.Params{Rollouts: 8})
			t.Logf("seed=%s pet=%d greedy=%d sampler=%d", seed, pet, g, s)
			if g <= 0 || s <= 0 {
				t.Errorf("seed=%s pet=%d: 终局分应为正 (greedy=%d sampler=%d)", seed, pet, g, s)
			}
		}
	}
}

// TestAIBenchmark 隐藏信息下的策略统计对比，按宠物刷新次数（用具刷新 0/2）分别统计，
// 并报告整局用具刷新使用次数（验证宠物提供的用具刷新确实被决策利用）。
// 运行: go test ./internal/application -run TestAIBenchmark -v
func TestAIBenchmark(t *testing.T) {
	svc := service(t)
	seeds := []string{"ai-b1", "ai-b2", "ai-b3", "ai-b4", "ai-b5", "ai-b6"}
	type petAgg struct {
		gSum, sSum, gToolRef, sToolRef int64
		gPotionRef, sPotionRef         int64
		n                              int
	}
	agg := map[int32]*petAgg{0: {}, 2: {}}
	for _, seed := range seeds {
		for _, pet := range []int32{0, 2} {
			g, gp, gt := playAI(t, svc, seed, pet, ai.StrategyGreedy, ai.Params{Samples: 24})
			s, sp, st := playAI(t, svc, seed, pet, ai.StrategySampler, ai.Params{Rollouts: 16})
			t.Logf("seed=%-8s pet=%d greedy=%-9d (药刷%d/具刷%d)  sampler=%-9d (药刷%d/具刷%d)",
				seed, pet, g, gp, gt, s, sp, st)
			a := agg[pet]
			a.gSum += g
			a.sSum += s
			a.gToolRef += int64(gt)
			a.sToolRef += int64(st)
			a.gPotionRef += int64(gp)
			a.sPotionRef += int64(sp)
			a.n++
		}
	}
	for _, pet := range []int32{0, 2} {
		a := agg[pet]
		n := int64(a.n)
		t.Logf("pet=%d: greedy平均=%d sampler平均=%d (sampler/greedy=%.2f) | 平均用具刷新使用: greedy=%d sampler=%d | 平均药剂刷新使用: greedy=%d sampler=%d",
			pet, a.gSum/n, a.sSum/n, float64(a.sSum)/float64(a.gSum), a.gToolRef/n, a.sToolRef/n, a.gPotionRef/n, a.sPotionRef/n)
	}
}
