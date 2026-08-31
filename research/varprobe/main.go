// 一次性实验：拆解 sampler 的方差来源。
// 同一种子重复对局（AI 内部采样是随机的），对比"种子间差异"与"同种子采样噪声"。
// 运行: go run ./research/varprobe <seed> <pet> <rollouts> <runs>
package main

import (
	"fmt"
	"os"
	"strconv"

	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func play(seed string, pet, rollouts int) int64 {
	r := engine.DemoRules()
	s, err := engine.New("run", "user", seed, int32(pet), r)
	if err != nil {
		return 0
	}
	for s.Phase != pb.Phase_FINISHED {
		obs := ai.FromGameState(s)
		act, err := ai.Decide(obs, ai.StrategySampler, ai.Params{Rollouts: rollouts})
		if err != nil {
			break
		}
		cmd := &pb.Command{Type: act.Type, CardId: act.CardID, OfferId: s.Offer.Id}
		for _, idx := range act.Slots {
			if s.Slots[idx].Monster != nil {
				cmd.TargetIds = append(cmd.TargetIds, s.Slots[idx].Monster.Id)
			}
		}
		next, _, err := engine.Apply(s, cmd, r)
		if err != nil {
			break
		}
		s = next
	}
	return s.Score
}

func main() {
	seed := "ai-b6"
	pet, rollouts, runs := 2, 8, 6
	if len(os.Args) > 1 {
		seed = os.Args[1]
	}
	if len(os.Args) > 2 {
		pet, _ = strconv.Atoi(os.Args[2])
	}
	if len(os.Args) > 3 {
		rollouts, _ = strconv.Atoi(os.Args[3])
	}
	if len(os.Args) > 4 {
		runs, _ = strconv.Atoi(os.Args[4])
	}
	xs := make([]int64, 0, runs)
	for i := 0; i < runs; i++ {
		xs = append(xs, play(seed, pet, rollouts))
	}
	sum, mn, mx := int64(0), xs[0], xs[0]
	for _, v := range xs {
		sum += v
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	mean := float64(sum) / float64(len(xs))
	ss := 0.0
	for _, v := range xs {
		d := float64(v) - mean
		ss += d * d
	}
	sd := 0.0
	if len(xs) > 1 {
		sd = sqrt(ss / float64(len(xs)-1))
	}
	fmt.Printf("seed=%s pet=%d rollouts=%d runs=%d\n", seed, pet, rollouts, runs)
	fmt.Printf("  分数: %v\n", xs)
	fmt.Printf("  均值=%d 最低=%d 最高=%d 样本标准差=%.0f (CV=%.1f%%)\n", int64(mean), mn, mx, sd, sd/mean*100)
}

func sqrt(x float64) float64 {
	z := x
	for i := 0; i < 64; i++ {
		z = (z + x/z) / 2
	}
	return z
}
