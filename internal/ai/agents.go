package ai

import (
	"fmt"
	"math/rand"
)

// Strategy 是 AI 决策策略。
type Strategy string

const (
	StrategyRandom      Strategy = "random"       // 随机基线
	StrategyGreedy      Strategy = "greedy"       // 期望即时分最大（玩家直觉）
	StrategySampler     Strategy = "sampler"      // 采样 rollout：期望终局分最大（隐藏信息下的近似最优）
	StrategyTierSampler Strategy = "tier_sampler" // 采样 rollout：优先提高保底率，再提高优秀率
)

// Params 是策略参数（可选）。
type Params struct {
	Samples  int // greedy 每个动作的抽样次数；默认 24
	Rollouts int // sampler 每个动作的完整对局抽样次数；默认 16
}

// Decide 是唯一决策入口：输入可见观察，输出一个合法动作。
// 签名只接受 *Observation —— 信息边界在类型层面成立，AI 代码接触不到隐藏状态。
func Decide(o *Observation, s Strategy, p Params) (*Action, error) {
	if o == nil {
		return nil, fmt.Errorf("观察为空")
	}
	if o.Done() {
		return nil, fmt.Errorf("对局已结束")
	}
	acts := LegalActionsFromObservation(o)
	if len(acts) == 0 {
		return nil, fmt.Errorf("当前没有可执行的动作")
	}
	switch s {
	case StrategyRandom:
		return acts[rand.Intn(len(acts))], nil
	case StrategyGreedy:
		return decideGreedy(o, acts, clamp(p.Samples, 1, 128, 24))
	case StrategySampler:
		return decideSampler(o, acts, clamp(p.Rollouts, 1, 64, 16), func(score int64) float64 { return float64(score) }, false)
	case StrategyTierSampler:
		return decideSampler(o, acts, clamp(p.Rollouts, 1, 64, 16), tierUtility, true)
	default:
		return nil, fmt.Errorf("未知策略 %q（可选: random / greedy / sampler / tier_sampler）", s)
	}
}

func clamp(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// decideGreedy：对每个候选动作做 samples 次一步推演（每次推演 = 抽样一个可能的未来），
// 取"动作后的可见分数"平均值最大者。平局时偏好 选卡 > 刷新 > 跳过。
func decideGreedy(o *Observation, acts []*Action, samples int) (*Action, error) {
	futures := make([]*simEnv, samples)
	for i := range futures {
		sim, err := buildSimSample(o, i)
		if err != nil {
			return nil, err
		}
		futures[i] = sim
	}
	best, bestAct := -1.0, (*Action)(nil)
	for _, a := range acts {
		sum := int64(0)
		for i := 0; i < samples; i++ {
			sim := futures[i].clone()
			if next, err := sim.step(a); err == nil {
				sum += next.Score
			}
		}
		avg := float64(sum) / float64(samples)
		if better(avg, a, best, bestAct) {
			best, bestAct = avg, a
		}
	}
	if bestAct == nil {
		return nil, fmt.Errorf("所有候选动作均无法执行")
	}
	return bestAct, nil
}

// decideSampler：对每个候选动作做 rollouts 次"完整贪心对局"推演
// （每次推演抽样一个未来，内部用贪心-1 打完一整局），取终局分期望最大者。
// 评估的是"这步棋打完整局的期望终局分"，能捕获先降后升的复利组合。
func decideSampler(o *Observation, acts []*Action, rollouts int, objective func(int64) float64, guided bool) (*Action, error) {
	playbook := lockedPlaybook(o)
	if guided {
		if action := lockedOpeningAction(playbook, o, acts); action != nil {
			return action, nil
		}
	}
	futures := make([]*simEnv, rollouts)
	for i := range futures {
		sim, err := buildSimSample(o, i)
		if err != nil {
			return nil, err
		}
		futures[i] = sim
	}
	best, bestAct := -1.0, (*Action)(nil)
	advantages := guideAdvantages(playbook, o, acts)
	for actionIndex, a := range acts {
		sum := 0.0
		for i := 0; i < rollouts; i++ {
			// 各动作共享同一组样本（common random numbers），减少动作间比较方差。
			sim := futures[i].clone()
			if _, err := sim.step(a); err != nil {
				continue
			}
			if guided {
				sum += objective(guidedTierRollout(sim))
			} else {
				sum += objective(greedyRollout(sim))
			}
		}
		avg := sum / float64(rollouts)
		if guided {
			// 小权重只用于稳定采样接近时的选择，不覆盖完整对局的档位结果。
			avg += 0.2 * advantages[actionIndex]
		}
		if better(avg, a, best, bestAct) {
			best, bestAct = avg, a
		}
	}
	if bestAct == nil {
		return nil, fmt.Errorf("所有候选动作均无法执行")
	}
	return bestAct, nil
}

// guidedTierRollout 在模拟未来中持续恢复开局用具锁定的流派，并用攻略偏好
// 打破短期档位效用接近的动作，避免每个模拟回合重新漂移到另一套构筑。
func guidedTierRollout(s *simEnv) int64 {
	guard := 0
	for {
		o := s.observe()
		if o.Done() || guard > 64 {
			return o.Score
		}
		guard++
		actions := LegalActionsFromObservation(o)
		advantages := guideAdvantages(lockedPlaybook(o), o, actions)
		best, bestSim := -1.0, (*simEnv)(nil)
		for index, action := range actions {
			candidate := s.clone()
			next, err := candidate.step(action)
			if err != nil {
				continue
			}
			value := tierUtility(next.Score) + 0.2*advantages[index]
			if value > best {
				best, bestSim = value, candidate
			}
		}
		if bestSim == nil {
			return o.Score
		}
		s = bestSim
	}
}

// tierUtility 与外部 PPO 的保底优先目标一致。607k 以下保留连续进度，
// 跨保底奖励 6，跨优秀额外奖励 2，1120k 以上不再增加价值。
func tierUtility(score int64) float64 {
	const floor, excellent, cap = int64(607_000), int64(721_000), int64(1_120_000)
	value := score
	if value < 0 {
		value = 0
	}
	lowProgress := float64(min(value, floor)) / float64(floor)
	if value < floor {
		return lowProgress
	}
	floorProgress := float64(min(value-floor, excellent-floor)) / float64(excellent-floor)
	result := lowProgress + 6 + floorProgress
	if value < excellent {
		return result
	}
	excellentProgress := float64(min(value-excellent, cap-excellent)) / float64(cap-excellent)
	return result + 2 + excellentProgress
}

// better 比较期望值；相等时偏好 选卡 > 刷新 > 跳过（避免无意义的平局偏向）。
func better(avg float64, a *Action, best float64, bestAct *Action) bool {
	if bestAct == nil || avg > best+1e-9 {
		return true
	}
	if avg < best-1e-9 {
		return false
	}
	return actionPriority(a) > actionPriority(bestAct)
}

func actionPriority(a *Action) int {
	switch a.Type {
	case "choose":
		return 2
	case "refresh":
		return 1
	default:
		return 0
	}
}

// greedyRollout 从推演环境出发，用"即时分最优"策略打完一整局（推演内部同样只靠观察）。
// 基于该推演环境已固定的随机未来，结果是确定的一条未来轨迹。
func greedyRollout(s *simEnv) int64 {
	guard := 0
	for {
		o := s.observe()
		if o.Done() || guard > 64 {
			return o.Score
		}
		guard++
		best, bestSim := int64(-1), (*simEnv)(nil)
		for _, a := range LegalActionsFromObservation(o) {
			c := s.clone()
			if next, err := c.step(a); err == nil && next.Score > best {
				best, bestSim = next.Score, c
			}
		}
		if bestSim == nil {
			return o.Score
		}
		s = bestSim
	}
}
