// 命令行基准：sampler（隐藏信息）跑 N 局随机种子对局，输出每局分数与完整统计。
// 运行示例：
//   go run ./research/bench                     # 100 局 sampler, pet=0, rollouts=16
//   go run ./research/bench -pet 2 -games 100   # pet=2 100 局
//   go run ./research/bench -pet 0,2 -games 50  # pet=0 和 2 各 50 局
//   go run ./research/bench -greedy             # 附带 greedy 对照
//   go run ./research/bench -rollouts 32        # 提高采样数降方差
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

var (
	games    = flag.Int("games", 100, "每种 pet 的对局数")
	pets     = flag.String("pet", "0", "宠物刷新次数，逗号分隔（如 0 或 0,2）")
	rollouts = flag.Int("rollouts", 16, "sampler 每动作的 rollout 数")
	greedy   = flag.Bool("greedy", false, "同时跑 greedy 作为对照")
	workers  = flag.Int("parallel", 0, "并行度（0 = 自动取 CPU 数）")
	out      = flag.String("out", "", "结果写入文件（空 = 仅终端）")
)

type result struct {
	seed  string
	pet   int32
	score int64
}

func play(seed string, pet int32, rollouts int, useGreedy bool) int64 {
	r := engine.DemoRules()
	s, err := engine.New("run", "user", seed, pet, r)
	if err != nil {
		return 0
	}
	strategy := ai.StrategySampler
	params := ai.Params{Rollouts: rollouts}
	if useGreedy {
		strategy = ai.StrategyGreedy
		params = ai.Params{Samples: 24}
	}
	for s.Phase != pb.Phase_FINISHED {
		obs := ai.FromGameState(s)
		act, err := ai.Decide(obs, strategy, params)
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
	flag.Parse()
	workersN := *workers
	if workersN <= 0 {
		workersN = runtime.NumCPU()
	}
	petList := []int32{}
	for _, p := range strings.Split(*pets, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var v int
		if _, err := fmt.Sscanf(p, "%d", &v); err != nil || v < 0 || v > 2 {
			fmt.Fprintf(os.Stderr, "无效 pet %q（应为 0/1/2）\n", p)
			os.Exit(1)
		}
		petList = append(petList, int32(v))
	}
	if len(petList) == 0 {
		petList = []int32{0}
	}

	var buf strings.Builder
	line := func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		fmt.Print(s)
		buf.WriteString(s)
	}

	line("sampler %d 局随机种子 · pet=%v · rollouts=%d · 并行 %d · 开始 %s\n",
		*games, petList, *rollouts, workersN, time.Now().Format("15:04:05"))
	start := time.Now()

	type runKey struct {
		pet   int32
		greed bool
	}
	perKey := map[runKey][]result{}
	greedModes := []bool{false}
	if *greedy {
		greedModes = append(greedModes, true)
	}
	var mu sync.Mutex
	sem := make(chan struct{}, workersN)
	var wg sync.WaitGroup
	for _, pet := range petList {
		for _, greed := range greedModes {
			for i := 0; i < *games; i++ {
				wg.Add(1)
				sem <- struct{}{}
				go func(i int, pet int32, greed bool) {
					defer wg.Done()
					defer func() { <-sem }()
					seed := fmt.Sprintf("%016x", rand.Uint64())
					score := play(seed, pet, *rollouts, greed)
					mu.Lock()
					perKey[runKey{pet, greed}] = append(perKey[runKey{pet, greed}], result{seed, pet, score})
					mu.Unlock()
				}(i, pet, greed)
			}
		}
	}
	wg.Wait()

	for _, pet := range petList {
		for _, greed := range greedModes {
			label := fmt.Sprintf("sampler(%d)", *rollouts)
			if greed {
				label = "greedy(24)"
			}
			rs := perKey[runKey{pet, greed}]
			line("\n=== pet=%d %s · %d 局 ===\n", pet, label, len(rs))
			scores := make([]int64, 0, len(rs))
			for _, r := range rs {
				scores = append(scores, r.score)
				line("  [%03d] seed=%s score=%d\n", len(scores), r.seed, r.score)
			}
			stats := summarize(scores)
			line("  统计: n=%d 均值=%.0f 样本方差=%.0f 样本标准差=%.0f CV=%.1f%%\n", stats.n, stats.mean, stats.sampleVar, stats.sd, stats.cv)
			line("        最低=%d 最高=%d 中位数=%d P10=%d P90=%d\n", stats.min, stats.max, stats.median, stats.p10, stats.p90)
		}
	}
	line("\n耗时 %s\n", time.Since(start).Round(time.Second))

	if *out != "" {
		if err := os.WriteFile(*out, []byte(buf.String()), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "写文件失败: %v\n", err)
		} else {
			fmt.Printf("结果已保存到 %s\n", *out)
		}
	}
}

type summary struct {
	n                         int
	mean, sampleVar, sd, cv   float64
	min, max, median, p10, p90 int64
}

func summarize(xs []int64) summary {
	sorted := append([]int64{}, xs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	s := summary{n: len(sorted), min: sorted[0], max: sorted[len(sorted)-1]}
	var sum int64
	for _, v := range sorted {
		sum += v
	}
	s.mean = float64(sum) / float64(len(sorted))
	ss := 0.0
	for _, v := range sorted {
		d := float64(v) - s.mean
		ss += d * d
	}
	s.sampleVar = ss / float64(len(sorted)-1)
	s.sd = sqrt(s.sampleVar)
	s.cv = s.sd / s.mean * 100
	s.median = percentile(sorted, 0.5)
	s.p10 = percentile(sorted, 0.1)
	s.p90 = percentile(sorted, 0.9)
	return s
}

func percentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)-1))
	return sorted[idx]
}

func sqrt(x float64) float64 {
	if x <= 0 {
		return 0
	}
	z := x
	for i := 0; i < 64; i++ {
		z = (z + x/z) / 2
	}
	return z
}
