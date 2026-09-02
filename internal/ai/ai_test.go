package ai

import (
	"encoding/json"
	"strings"
	"testing"

	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func newFixture(t *testing.T) *pb.GameState {
	t.Helper()
	s, err := engine.New("run", "user", "ai-test-seed", 2, rules)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// TestInformationBoundary 验证信息边界：观察的 JSON 序列化绝不包含隐藏字段。
func TestInformationBoundary(t *testing.T) {
	s := newFixture(t)
	obs := FromGameState(s)
	raw, err := json.Marshal(obs)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, forbidden := range []string{"seed", "initRng", "offerRng", "effectRng", "runId", "stateToken", "rngVersion", "nextMonsterId", "nextOfferId"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("观察泄露了隐藏字段 %q: %s", forbidden, text)
		}
	}
	for _, required := range []string{"slots", "cards", "score", "tools", "rewards", "phase", "baseCursor"} {
		if !strings.Contains(text, required) {
			t.Errorf("观察缺少可见字段 %q", required)
		}
	}
}

// TestObservationRoundTrip 验证 观察 → 重建状态 → 观察 的公开字段保持一致。
func TestObservationRoundTrip(t *testing.T) {
	s := newFixture(t)
	// 推进到 CHOOSING（跳过未知器具，进入开局用具选择）
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	rebuilt, err := RebuildState(obs)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if err := engine.ValidateState(rebuilt, rules); err != nil {
		t.Fatalf("重建状态不合法: %v", err)
	}
	obs2 := FromGameState(rebuilt)
	if obs2.Score != obs.Score || obs2.BaseCursor != obs.BaseCursor || obs2.CompletedTurns != obs.CompletedTurns ||
		obs2.PotionRefreshes != obs.PotionRefreshes || obs2.ToolRefreshes != obs.ToolRefreshes {
		t.Errorf("重建后流程字段不一致: %+v vs %+v", obs, obs2)
	}
	if len(obs2.Slots) != len(obs.Slots) {
		t.Fatalf("槽位数不一致")
	}
	for i := range obs.Slots {
		a, b := obs.Slots[i], obs2.Slots[i]
		if a.DefinitionID != b.DefinitionID || a.Name != b.Name || a.Family != b.Family || a.Rarity != b.Rarity || a.Activity != b.Activity || a.Quantity != b.Quantity {
			t.Errorf("槽位 %d 不一致: %+v vs %+v", i, a, b)
		}
	}
	if len(obs2.Cards) != len(obs.Cards) {
		t.Errorf("候选卡不一致: %d vs %d", len(obs2.Cards), len(obs.Cards))
	}
}

// TestRebuildStateDistinctFutures 验证公平性：同一观察重建出的推演状态各不相同（随机未来）。
func TestRebuildStateDistinctFutures(t *testing.T) {
	s := newFixture(t)
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	seen := map[uint64]bool{}
	for i := 0; i < 8; i++ {
		rebuilt, err := RebuildState(obs)
		if err != nil {
			t.Fatal(err)
		}
		seen[rebuilt.OfferRng] = true
	}
	if len(seen) < 2 {
		t.Errorf("推演环境 RNG 未随机化，隐藏信息形同虚设")
	}
}

func TestBuildSimSampleIsRepeatable(t *testing.T) {
	s := newFixture(t)
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	a, err := buildSimSample(obs, 3)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildSimSample(obs, 3)
	if err != nil {
		t.Fatal(err)
	}
	if a.state.InitRng != b.state.InitRng || a.state.OfferRng != b.state.OfferRng ||
		a.state.EffectRng != b.state.EffectRng || a.state.OpeningToolFamily != b.state.OpeningToolFamily {
		t.Fatal("相同公开局面和样本编号必须生成相同推演未来")
	}
	c, err := buildSimSample(obs, 4)
	if err != nil {
		t.Fatal(err)
	}
	if a.state.InitRng == c.state.InitRng && a.state.OfferRng == c.state.OfferRng && a.state.EffectRng == c.state.EffectRng {
		t.Fatal("不同样本编号不应生成完全相同的推演未来")
	}
}

func TestTierSamplerDecisionIsRepeatable(t *testing.T) {
	s := newFixture(t)
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	want, err := Decide(obs, StrategyTierSampler, Params{Rollouts: 2})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := Decide(obs, StrategyTierSampler, Params{Rollouts: 2})
		if err != nil {
			t.Fatal(err)
		}
		if got.Type != want.Type || got.CardID != want.CardID || !equalInts(got.Slots, want.Slots) {
			t.Fatalf("同一公开局面决策不稳定: want=%v got=%v", want, got)
		}
	}
}

// TestLegalActions 验证动作枚举与"观察即合法动作全集"。
func TestLegalActions(t *testing.T) {
	s := newFixture(t)
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	acts := LegalActionsFromObservation(obs)
	if len(acts) == 0 {
		t.Fatal("开局用具选择不应没有动作")
	}
	for _, a := range acts {
		sim, err := buildSim(obs)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sim.step(a); err != nil {
			t.Errorf("枚举出的动作 %v 无法执行: %v", a, err)
		}
	}
}

// TestDecideStrategies 所有策略都应返回可执行动作。
func TestDecideStrategies(t *testing.T) {
	s := newFixture(t)
	next, _, err := engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	obs := FromGameState(next)
	for _, strat := range []Strategy{StrategyRandom, StrategyGreedy, StrategySampler, StrategyTierSampler} {
		act, err := Decide(obs, strat, Params{})
		if err != nil {
			t.Fatalf("策略 %s 失败: %v", strat, err)
		}
		valid := false
		for _, la := range LegalActionsFromObservation(obs) {
			if la.Type == act.Type && la.CardID == act.CardID && equalInts(la.Slots, act.Slots) {
				valid = true
				break
			}
		}
		if !valid {
			t.Errorf("策略 %s 返回了非法动作 %v", strat, act)
		}
	}
}

func TestTierUtilityPrioritizesFloorAndCapsExcess(t *testing.T) {
	if got := tierUtility(607_000); got != 7 {
		t.Fatalf("floor utility = %v, want 7", got)
	}
	if got := tierUtility(721_000); got != 10 {
		t.Fatalf("excellent utility = %v, want 10", got)
	}
	if tierUtility(3_000_000) != tierUtility(1_120_000) {
		t.Fatal("scores above cap must have equal utility")
	}
}

// TestGreedyChoosesBestImmediate 确定性局面下，greedy 应选即时分最高的动作。
func TestGreedyChoosesBestImmediate(t *testing.T) {
	// 构造：槽0 有 15x12 稀有骨卫兵；候选 [脊髓溶液-觉醒者(添加觉醒者), 迷魂酊剂(觉醒槽0)]。
	// 觉醒 15x12 → 315x13 = 4095 分；添加觉醒者 ≤ 36x24+180 = 1044 分。觉醒应胜出。
	s, err := engine.New("run", "user", "greedy-seed", 2, rules)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = pb.Phase_CHOOSING
	s.OpeningToolFamily = pb.Family_BONE
	s.Slots[0].Monster = &pb.Monster{Id: "m0", Family: pb.Family_BONE, Rarity: pb.MonsterRarity_RARE, Activity: 15, Quantity: 12}
	s.Offer = &pb.Offer{Id: "off", Kind: pb.CardKind_POTION, CardIds: []string{"awaker_fluid", "awakening"}}
	obs := FromGameState(s)
	act, err := Decide(obs, StrategyGreedy, Params{Samples: 8})
	if err != nil {
		t.Fatal(err)
	}
	if act.CardID != "awakening" {
		t.Errorf("greedy 应选觉醒卡，得到 %v", act)
	}
}

func equalInts(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ---- 刷新次数语义：药剂固定 3 次 / 用具 = 宠物提供的 0/1/2 次 ----

// TestRefreshCountersDistinct 验证引擎语义与观察映射、重建都严格区分两类刷新。
func TestRefreshCountersDistinct(t *testing.T) {
	for _, pet := range []int32{0, 2} {
		s, err := engine.New("run", "user", "refresh-seed", pet, rules)
		if err != nil {
			t.Fatal(err)
		}
		// 引擎：药剂刷新固定 3 次，用具刷新 = 宠物次数。
		if s.PotionRefreshes != 3 {
			t.Errorf("pet=%d: 药剂刷新应固定 3 次，得到 %d", pet, s.PotionRefreshes)
		}
		if s.ToolRefreshes != pet {
			t.Errorf("pet=%d: 用具刷新应等于宠物次数 %d，得到 %d", pet, pet, s.ToolRefreshes)
		}
		// 观察映射。
		obs := FromGameState(s)
		if obs.PotionRefreshes != 3 || obs.ToolRefreshes != pet {
			t.Errorf("pet=%d: 观察映射错误 potion=%d tool=%d", pet, obs.PotionRefreshes, obs.ToolRefreshes)
		}
		// 重建保持。
		rebuilt, err := RebuildState(obs)
		if err != nil {
			t.Fatal(err)
		}
		if rebuilt.PotionRefreshes != 3 || rebuilt.ToolRefreshes != pet {
			t.Errorf("pet=%d: 重建错误 potion=%d tool=%d", pet, rebuilt.PotionRefreshes, rebuilt.ToolRefreshes)
		}
	}
}

// TestRefreshAvailabilityByKindAndCounters 验证刷新可用性由"候选类别 + 对应计数器"决定，
// 且不信任外部传入的 CanRefresh 标志（防止 observation 模式传错）。
func TestRefreshAvailabilityByKindAndCounters(t *testing.T) {
	cases := []struct {
		name                  string
		offerKind             int32
		potion, tool          int32
		canRefreshFlag        bool
		wantRefresh, wantSkip bool
	}{
		{"药剂候选+药剂刷新>0", 2, 3, 0, true, true, false},
		{"药剂候选+药剂刷新=0 但用具刷新>0", 2, 0, 2, true, false, false},
		{"用具候选+用具刷新>0", 3, 0, 2, true, true, false},
		{"用具候选+用具刷新=0 但药剂刷新>0", 3, 3, 0, true, false, false},
		{"方案候选不可刷新", 4, 3, 2, false, false, false},
		{"标志错误也被纠正(计数为0)", 2, 0, 0, true, false, false},
		{"准备阶段可跳过", 1, 3, 2, true, false, true},
	}
	for _, tc := range cases {
		o := &Observation{
			Phase:           map[int32]string{1: "PREPARING"}[tc.offerKind],
			Offer:           OfferView{Kind: tc.offerKind},
			PotionRefreshes: tc.potion, ToolRefreshes: tc.tool, CanRefresh: tc.canRefreshFlag,
		}
		if o.Phase == "" {
			o.Phase = "CHOOSING"
		}
		has := func(typ string) bool {
			for _, a := range LegalActionsFromObservation(o) {
				if a.Type == typ {
					return true
				}
			}
			return false
		}
		if has("refresh") != tc.wantRefresh {
			t.Errorf("%s: 期望有刷新=%v 实际=%v", tc.name, tc.wantRefresh, has("refresh"))
		}
		if has("skip_unknown") != tc.wantSkip {
			t.Errorf("%s: 期望可跳过=%v 实际=%v", tc.name, tc.wantSkip, has("skip_unknown"))
		}
	}
}

// TestRefreshConsumesCorrectCounter 验证推演中 refresh 只消耗与候选类别匹配的计数器。
func TestRefreshConsumesCorrectCounter(t *testing.T) {
	// 药剂候选：refresh 只减药剂刷新。
	s, _ := engine.New("run", "user", "r1", 2, rules)
	s.Phase = pb.Phase_CHOOSING
	s.OpeningToolFamily = pb.Family_BONE
	s.Slots[0].Monster = &pb.Monster{Id: "m0", Family: pb.Family_BONE, Rarity: pb.MonsterRarity_NORMAL, Activity: 1, Quantity: 36}
	s.Offer = &pb.Offer{Id: "off", Kind: pb.CardKind_POTION, CardIds: []string{"awaker_fluid"}}
	sim, err := buildSim(FromGameState(s))
	if err != nil {
		t.Fatal(err)
	}
	o2, err := sim.step(&Action{Type: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if o2.PotionRefreshes != 2 {
		t.Errorf("药剂候选刷新后药剂刷新应 3→2，得到 %d", o2.PotionRefreshes)
	}
	if o2.ToolRefreshes != 2 {
		t.Errorf("药剂候选刷新不应消耗用具刷新，得到 %d", o2.ToolRefreshes)
	}

	// 用具候选：refresh 只减用具刷新。
	s2, _ := engine.New("run", "user", "r2", 2, rules)
	s2.Phase = pb.Phase_CHOOSING
	s2.OpeningToolFamily = pb.Family_BONE
	s2.Slots[0].Monster = &pb.Monster{Id: "m0", Family: pb.Family_BONE, Rarity: pb.MonsterRarity_NORMAL, Activity: 1, Quantity: 36}
	s2.Offer = &pb.Offer{Id: "off2", Kind: pb.CardKind_TOOL, CardIds: []string{"claw", "pupa", "marrow"}, RewardThreshold: 0}
	sim2, err := buildSim(FromGameState(s2))
	if err != nil {
		t.Fatal(err)
	}
	o3, err := sim2.step(&Action{Type: "refresh"})
	if err != nil {
		t.Fatal(err)
	}
	if o3.ToolRefreshes != 1 {
		t.Errorf("用具候选刷新后用具刷新应 2→1，得到 %d", o3.ToolRefreshes)
	}
	if o3.PotionRefreshes != 3 {
		t.Errorf("用具候选刷新不应消耗药剂刷新，得到 %d", o3.PotionRefreshes)
	}
}
