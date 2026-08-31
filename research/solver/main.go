// 实验性求解器 v2：用真实引擎作为模拟器，对比随机 / 贪心 / 束搜索，支持并行。
// 运行: go run ./research/solver <seed...>
package main

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

type counter struct{ v int64 }

func (c *counter) add()      { atomic.AddInt64(&c.v, 1) }
func (c *counter) value() int64 { return atomic.LoadInt64(&c.v) }

func legalActions(s *pb.GameState, r *engine.Rules) []*pb.Command {
	var cmds []*pb.Command
	if s.Phase == pb.Phase_PREPARING {
		cmds = append(cmds, &pb.Command{Type: "skip_unknown"})
	}
	if s.Offer == nil {
		return cmds
	}
	if s.Offer.Kind == pb.CardKind_POTION && s.PotionRefreshes > 0 {
		cmds = append(cmds, &pb.Command{Type: "refresh"})
	}
	if s.Offer.Kind == pb.CardKind_TOOL && s.ToolRefreshes > 0 {
		cmds = append(cmds, &pb.Command{Type: "refresh"})
	}
	for _, id := range s.Offer.CardIds {
		card := r.Card(id)
		if card == nil || !card.Enabled {
			continue
		}
		for _, t := range engine.LegalTargets(s, card) {
			cmds = append(cmds, &pb.Command{Type: "choose", CardId: id, TargetIds: t.Ids})
		}
	}
	return cmds
}

func applyCmd(s *pb.GameState, cmd *pb.Command, r *engine.Rules) *pb.GameState {
	c := &pb.Command{Type: cmd.Type, CardId: cmd.CardId, TargetIds: cmd.TargetIds, OfferId: s.Offer.Id}
	next, _, err := engine.Apply(s, c, r)
	if err != nil {
		return nil
	}
	return next
}

// ---- 贪心 ----

func value(s *pb.GameState, r *engine.Rules, depth int, stats *counter) int64 {
	if s.Phase == pb.Phase_FINISHED || depth == 0 {
		return s.Score
	}
	stats.add()
	best := int64(-1)
	for _, cmd := range legalActions(s, r) {
		if next := applyCmd(s, cmd, r); next != nil {
			if v := value(next, r, depth-1, stats); v > best {
				best = v
			}
		}
	}
	return best
}

func playGreedy(s *pb.GameState, r *engine.Rules, depth int, stats *counter) *pb.GameState {
	cur := s
	for cur.Phase != pb.Phase_FINISHED {
		best, bestNext := int64(-1), (*pb.GameState)(nil)
		for _, cmd := range legalActions(cur, r) {
			if next := applyCmd(cur, cmd, r); next != nil {
				if v := value(next, r, depth-1, stats); v > best {
					best, bestNext = v, next
				}
			}
		}
		if bestNext == nil {
			break
		}
		cur = bestNext
	}
	return cur
}

// ---- rollout（贪心打完 / 有限深度） ----

// rolloutPlies 从 s 出发用贪心-1 走 n 层（n<=0 表示打完），返回最终得分。
func rolloutPlies(s *pb.GameState, r *engine.Rules, plies int, stats *counter) int64 {
	cur := s
	steps := 0
	for cur.Phase != pb.Phase_FINISHED && (plies <= 0 || steps < plies) {
		steps++
		best, bestNext := int64(-1), (*pb.GameState)(nil)
		for _, cmd := range legalActions(cur, r) {
			if next := applyCmd(cur, cmd, r); next != nil {
				stats.add()
				if next.Score > best {
					best, bestNext = next.Score, next
				}
			}
		}
		if bestNext == nil {
			break
		}
		cur = bestNext
	}
	return cur.Score
}

// ---- 束搜索 ----

type evalFn func(*pb.GameState) int64

func playBeam(s *pb.GameState, r *engine.Rules, width int, evals []evalFn, stats *counter) *pb.GameState {
	beam := []*pb.GameState{s}
	globalBest := s
	guard := 0
	workers := runtime.NumCPU()
	for {
		allDone := true
		for _, st := range beam {
			if st.Phase != pb.Phase_FINISHED {
				allDone = false
				break
			}
		}
		if allDone || guard > 64 {
			break
		}
		guard++

		// 并行展开当前束
		var mu sync.Mutex
		next := []*pb.GameState{}
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, st := range beam {
			if st.Phase == pb.Phase_FINISHED {
				mu.Lock()
				next = append(next, st)
				mu.Unlock()
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(st *pb.GameState) {
				defer wg.Done()
				defer func() { <-sem }()
				local := []*pb.GameState{}
				for _, cmd := range legalActions(st, r) {
					if n := applyCmd(st, cmd, r); n != nil {
						stats.add()
						if n.Phase == pb.Phase_FINISHED {
							mu.Lock()
							if n.Score > globalBest.Score {
								globalBest = n
							}
							mu.Unlock()
						}
						local = append(local, n)
					}
				}
				mu.Lock()
				next = append(next, local...)
				mu.Unlock()
			}(st)
		}
		wg.Wait()

		if len(evals) > 0 {
			// 并行计算启发值
			scores := make([]int64, len(next))
			var wg2 sync.WaitGroup
			sem2 := make(chan struct{}, workers)
			for i, st := range next {
				wg2.Add(1)
				sem2 <- struct{}{}
				go func(i int, st *pb.GameState) {
					defer wg2.Done()
					defer func() { <-sem2 }()
					var best int64
					for _, ev := range evals {
						if v := ev(st); v > best {
							best = v
						}
					}
					scores[i] = best
				}(i, st)
			}
			wg2.Wait()
			idx := make([]int, len(next))
			for i := range idx {
				idx[i] = i
			}
			sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
			if len(idx) > width {
				idx = idx[:width]
			}
			trimmed := make([]*pb.GameState, 0, len(idx))
			for _, i := range idx {
				trimmed = append(trimmed, next[i])
			}
			beam = trimmed
		} else {
			sort.Slice(next, func(i, j int) bool { return next[i].Score > next[j].Score })
			if len(next) > width {
				next = next[:width]
			}
			beam = next
		}
	}
	return globalBest
}

// ---- 追踪最优路径 ----

type traceNode struct {
	state *pb.GameState
	parent *traceNode
	cmd    string // 人类可读命令描述
}

func playBeamTraced(s *pb.GameState, r *engine.Rules, width int, evals []evalFn, stats *counter) (*pb.GameState, []string) {
	root := &traceNode{state: s}
	beam := []*traceNode{root}
	globalBest := root
	guard := 0
	workers := runtime.NumCPU()
	for {
		allDone := true
		for _, st := range beam {
			if st.state.Phase != pb.Phase_FINISHED {
				allDone = false
				break
			}
		}
		if allDone || guard > 64 {
			break
		}
		guard++
		var mu sync.Mutex
		next := []*traceNode{}
		sem := make(chan struct{}, workers)
		var wg sync.WaitGroup
		for _, st := range beam {
			if st.state.Phase == pb.Phase_FINISHED {
				mu.Lock()
				next = append(next, st)
				mu.Unlock()
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(st *traceNode) {
				defer wg.Done()
				defer func() { <-sem }()
				local := []*traceNode{}
				for _, cmd := range legalActions(st.state, r) {
					n := applyCmd(st.state, cmd, r)
					if n == nil {
						continue
					}
					stats.add()
					label := describe(st.state, cmd, n)
					child := &traceNode{state: n, parent: st, cmd: label}
					if n.Phase == pb.Phase_FINISHED {
						mu.Lock()
						if n.Score > globalBest.state.Score {
							globalBest = child
						}
						mu.Unlock()
					}
					local = append(local, child)
				}
				mu.Lock()
				next = append(next, local...)
				mu.Unlock()
			}(st)
		}
		wg.Wait()
		if len(evals) > 0 {
			scores := make([]int64, len(next))
			var wg2 sync.WaitGroup
			sem2 := make(chan struct{}, workers)
			for i, st := range next {
				wg2.Add(1)
				sem2 <- struct{}{}
				go func(i int, st *traceNode) {
					defer wg2.Done()
					defer func() { <-sem2 }()
					var best int64
					for _, ev := range evals {
						if v := ev(st.state); v > best {
							best = v
						}
					}
					scores[i] = best
				}(i, st)
			}
			wg2.Wait()
			idx := make([]int, len(next))
			for i := range idx {
				idx[i] = i
			}
			sort.Slice(idx, func(a, b int) bool { return scores[idx[a]] > scores[idx[b]] })
			if len(idx) > width {
				idx = idx[:width]
			}
			trimmed := make([]*traceNode, 0, len(idx))
			for _, i := range idx {
				trimmed = append(trimmed, next[i])
			}
			beam = trimmed
		} else {
			sort.Slice(next, func(i, j int) bool { return next[i].state.Score > next[j].state.Score })
			if len(next) > width {
				next = next[:width]
			}
			beam = next
		}
	}
	// 回溯路径
	path := []string{}
	for n := globalBest; n != nil && n.parent != nil; n = n.parent {
		path = append([]string{n.cmd}, path...)
	}
	return globalBest.state, path
}

func describe(prev *pb.GameState, cmd *pb.Command, next *pb.GameState) string {
	r := engine.DemoRules()
	card := r.Card(cmd.CardId)
	name := ""
	if card != nil {
		name = card.Name
	}
	stage := fmt.Sprintf("基础%d/11", next.BaseCursor)
	if next.Offer != nil && next.Offer.Kind == pb.CardKind_TOOL && next.Offer.RewardThreshold != 0 {
		stage = fmt.Sprintf("奖励%d分", next.Offer.RewardThreshold)
	}
	label := fmt.Sprintf("[回合%d %s] %s", next.CompletedTurns, stage, name)
	if len(cmd.TargetIds) > 0 {
		label += fmt.Sprintf(" 目标:%s", strings.Join(cmd.TargetIds, ","))
	}
	if cmd.Type == "refresh" {
		label = fmt.Sprintf("[回合%d %s] 刷新候选", next.CompletedTurns, stage)
	}
	if cmd.Type == "skip_unknown" {
		label = "[准备] 跳过未知器具"
	}
	label += fmt.Sprintf(" → 分数 %d", next.Score)
	return label
}

func main() {
	args := os.Args[1:]
	deep := false
	trace := false
	if len(args) > 0 && args[0] == "-deep" {
		deep = true
		args = args[1:]
	}
	if len(args) > 0 && args[0] == "-trace" {
		trace = true
		args = args[1:]
	}
	seeds := []string{"demo-1", "demo-2", "demo-3", "demo-4", "demo-5", "demo-6"}
	if len(args) > 0 {
		seeds = args
	}
	r := engine.DemoRules()

	if trace {
		// 追踪最优束的完整决策路径（roll4 启发，宽度 30）。
		for _, seed := range seeds {
			stats := &counter{}
			start := time.Now()
			s, _ := engine.New("run", "user", seed, 0, r)
			final, path := playBeamTraced(s, r, 30, []evalFn{
				func(st *pb.GameState) int64 { return rolloutPlies(st, r, 4, stats) },
				func(st *pb.GameState) int64 { return st.Score },
			}, stats)
			fmt.Printf("=== %s 最优路径（终局 %d 分, %d 节点, %d ms）===\n", seed, final.Score, stats.value(), time.Since(start).Milliseconds())
			for _, step := range path {
				fmt.Println("  " + step)
			}
			fmt.Printf("  终局槽位:")
			for _, slot := range final.Slots {
				if slot.Monster != nil {
					m := slot.Monster
					fmt.Printf(" [%s r%d a%d q%d]", m.Family, m.Rarity, m.Activity, m.Quantity)
				} else {
					fmt.Printf(" [空]")
				}
			}
			fmt.Println()
		}
		return
	}

	if deep {
		// 深探测：更宽的完整 rollout 束，观察是否继续提升（收敛性 → 逼近最优）。
		fmt.Printf("%-8s %-30s %12s %12s %10s\n", "seed", "player", "score", "nodes", "ms")
		for _, seed := range seeds {
			for _, pet := range []int32{0, 2} {
				for _, width := range []int{20, 40} {
					stats := &counter{}
					start := time.Now()
					s, _ := engine.New("run", "user", seed, pet, r)
					final := playBeam(s, r, width, []evalFn{
						func(st *pb.GameState) int64 { return rolloutPlies(st, r, 0, stats) },
					}, stats)
					fmt.Printf("%-8s %-30s %12d %12d %10d\n", seed, fmt.Sprintf("beam-%d-rollfull (pet=%d)", width, pet), final.Score, stats.value(), time.Since(start).Milliseconds())
				}
			}
		}
		return
	}

	fmt.Printf("%-8s %-30s %12s %12s %10s\n", "seed", "player", "score", "nodes", "ms")
	for _, seed := range seeds {
		for _, pet := range []int32{0, 2} {
			// 随机基线
			{
				best, sum, n := int64(0), int64(0), 0
				for i := 0; i < 200; i++ {
					s, _ := engine.New("run", "user", seed, pet, r)
					for s.Phase != pb.Phase_FINISHED {
						acts := legalActions(s, r)
						if len(acts) == 0 {
							break
						}
						s = applyCmd(s, acts[rand.Intn(len(acts))], r)
						if s == nil {
							break
						}
					}
					if s != nil {
						sum += s.Score
						if s.Score > best {
							best = s.Score
						}
						n++
					}
				}
				fmt.Printf("%-8s %-30s %12d %12d %10s\n", seed, fmt.Sprintf("random-200 avg (pet=%d)", pet), sum/int64(max(n, 1)), 0, "")
				fmt.Printf("%-8s %-30s %12d %12d %10s\n", seed, fmt.Sprintf("random-200 best (pet=%d)", pet), best, 0, "")
			}

			// 贪心 1 / 2 层
			for _, depth := range []int{1, 2} {
				stats := &counter{}
				start := time.Now()
				s, _ := engine.New("run", "user", seed, pet, r)
				final := playGreedy(s, r, depth, stats)
				fmt.Printf("%-8s %-30s %12d %12d %10d\n", seed, fmt.Sprintf("greedy-%d (pet=%d)", depth, pet), final.Score, stats.value(), time.Since(start).Milliseconds())
			}

			// 束搜索：纯分数
			for _, width := range []int{20, 200} {
				stats := &counter{}
				start := time.Now()
				s, _ := engine.New("run", "user", seed, pet, r)
				final := playBeam(s, r, width, nil, stats)
				fmt.Printf("%-8s %-30s %12d %12d %10d\n", seed, fmt.Sprintf("beam-%d-score (pet=%d)", width, pet), final.Score, stats.value(), time.Since(start).Milliseconds())
			}

			// 束搜索：有限深度 rollout 启发
			for _, width := range []int{10, 30} {
				stats := &counter{}
				start := time.Now()
				s, _ := engine.New("run", "user", seed, pet, r)
				final := playBeam(s, r, width, []evalFn{
					func(st *pb.GameState) int64 { return rolloutPlies(st, r, 4, stats) },
					func(st *pb.GameState) int64 { return st.Score },
				}, stats)
				fmt.Printf("%-8s %-30s %12d %12d %10d\n", seed, fmt.Sprintf("beam-%d-roll4 (pet=%d)", width, pet), final.Score, stats.value(), time.Since(start).Milliseconds())
			}

			// 束搜索：完整 rollout 启发（最强，最慢）
			stats := &counter{}
			start := time.Now()
			s, _ := engine.New("run", "user", seed, pet, r)
			final := playBeam(s, r, 10, []evalFn{
				func(st *pb.GameState) int64 { return rolloutPlies(st, r, 0, stats) },
			}, stats)
			fmt.Printf("%-8s %-30s %12d %12d %10d\n", seed, fmt.Sprintf("beam-10-rollfull (pet=%d)", pet), final.Score, stats.value(), time.Since(start).Milliseconds())
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
