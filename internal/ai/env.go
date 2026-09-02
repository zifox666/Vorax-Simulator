package ai

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"

	"google.golang.org/protobuf/proto"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

// Action 是 AI 输出的一条命令；目标用槽位序号表示（与 UI 一致）。
type Action struct {
	Type   string  `json:"type"`   // "choose" | "refresh" | "skip_unknown"
	CardID string  `json:"cardId"` // choose 时的候选卡 id
	Slots  []int32 `json:"targetSlots"`
}

func (a *Action) String() string {
	if a.Type == "refresh" {
		return "刷新候选"
	}
	if a.Type == "skip_unknown" {
		return "跳过未知器具"
	}
	return fmt.Sprintf("选卡 %s 目标槽 %v", a.CardID, a.Slots)
}

// LegalActionsFromObservation 从可见观察直接枚举合法动作（UI 上玩家能做出的全部选择）。
// 刷新可用性由"候选类别 + 剩余次数"推导，而不是信任外部传入的 CanRefresh 标志：
// 药剂候选只消耗药剂刷新（整局固定 3 次），用具候选只消耗用具刷新（宠物提供 0/1/2 次）。
func LegalActionsFromObservation(o *Observation) []*Action {
	var acts []*Action
	if o.Phase == "PREPARING" {
		acts = append(acts, &Action{Type: "skip_unknown"})
	}
	canRefresh := (o.Offer.Kind == int32(pb.CardKind_POTION) && o.PotionRefreshes > 0) ||
		(o.Offer.Kind == int32(pb.CardKind_TOOL) && o.ToolRefreshes > 0)
	if canRefresh {
		acts = append(acts, &Action{Type: "refresh"})
	}
	for _, c := range o.Cards {
		if !c.Playable {
			continue
		}
		for _, ts := range c.TargetSets {
			acts = append(acts, &Action{Type: "choose", CardID: c.ID, Slots: ts})
		}
	}
	return acts
}

// simEnv 是 AI 内部的推演环境：状态由 Observation 重建，RNG 被随机重播，
// 因此推演结果 = "未知未来"的一次抽样，而不是真实未来的确定重放。
type simEnv struct {
	state *pb.GameState
}

// RebuildState 从可见观察重建可推演状态。公开字段（槽位/用具/候选/进度/奖励）
// 与观察一致；隐藏字段（RNG、NextMonsterId、NextOfferId）用随机值，
// seed/runId 置空 —— AI 从不接触真实隐藏状态。
func RebuildState(o *Observation) (*pb.GameState, error) {
	s := &pb.GameState{
		Phase:           phaseOf(o.Phase),
		BaseCursor:      o.BaseCursor,
		CompletedTurns:  o.CompletedTurns,
		Score:           o.Score,
		PeakScore:       o.Score,
		PotionRefreshes: o.PotionRefreshes,
		ToolRefreshes:   o.ToolRefreshes,
		Rewards: &pb.RewardState{
			Jars:             toJarColors(o.Rewards.Jars),
			DropBonusPercent: o.Rewards.DropBonusPercent,
			NextThreshold:    o.Rewards.NextThreshold,
			NextRewardLabel:  o.Rewards.NextRewardLabel,
		},
		// 隐藏状态：随机重播，AI 无法借此得知真实未来。
		InitRng:        rand.Uint64(),
		OfferRng:       rand.Uint64(),
		EffectRng:      rand.Uint64(),
		NextMonsterId:  1,
		NextOfferId:    1,
		FormatVersion:  1,
		RulesVersion:   engine.RulesVersion,
		ContentVersion: engine.ContentVersion,
		RngVersion:     engine.RNGVersion,
		Revision:       1,
	}
	// 奖励用具领取状态（玩家可见的流程状态）默认 LOCKED，再按观察覆盖。
	s.Rewards.ToolClaims = []*pb.ToolClaim{{Threshold: 8000, Status: pb.ClaimStatus_LOCKED}, {Threshold: 28000, Status: pb.ClaimStatus_LOCKED}}
	for i, cl := range o.Rewards.ToolClaims {
		if i >= len(s.Rewards.ToolClaims) {
			break
		}
		s.Rewards.ToolClaims[i].Threshold = cl.Threshold
		s.Rewards.ToolClaims[i].Status = claimOf(cl.Status)
	}
	// 六个槽位。
	for _, sv := range o.Slots {
		slot := &pb.Slot{Index: sv.Index}
		if sv.Family >= 1 && sv.Family <= 4 && sv.Rarity >= 1 && sv.Rarity <= 4 {
			slot.Monster = &pb.Monster{
				DefinitionId: sv.DefinitionID,
				Name:         sv.Name,
				Id:           fmt.Sprintf("m-%d", sv.Index),
				Family:       pb.Family(sv.Family),
				Rarity:       pb.MonsterRarity(sv.Rarity),
				Activity:     sv.Activity,
				Quantity:     sv.Quantity,
			}
		}
		s.Slots = append(s.Slots, slot)
	}
	// 已拥有用具。
	for _, id := range o.Tools {
		if c := rules.Card(id); c != nil && c.Kind == pb.CardKind_TOOL {
			s.Tools = append(s.Tools, id)
		}
	}
	// 当前候选。
	if o.Phase != "FINISHED" && o.Offer.Kind != 0 {
		s.Offer = &pb.Offer{Id: "ai-offer", Kind: pb.CardKind(o.Offer.Kind), RewardThreshold: o.Offer.RewardThreshold, Source: "ai"}
		for _, c := range o.Cards {
			s.Offer.CardIds = append(s.Offer.CardIds, c.ID)
		}
	}
	// 开局流派：从槽位多数派推导（并列时用重建状态自己的随机）。
	s.OpeningToolFamily = derivedToolFamily(s)
	if err := engine.ValidateState(s, rules); err != nil {
		return nil, fmt.Errorf("观察无法重建可推演状态: %w", err)
	}
	return s, nil
}

func phaseOf(p string) pb.Phase {
	switch p {
	case "PREPARING":
		return pb.Phase_PREPARING
	case "FINISHED":
		return pb.Phase_FINISHED
	default:
		return pb.Phase_CHOOSING
	}
}

func toJarColors(xs []int32) []pb.JarColor {
	out := make([]pb.JarColor, 0, len(xs))
	for _, x := range xs {
		out = append(out, pb.JarColor(x))
	}
	return out
}

func claimOf(s string) pb.ClaimStatus {
	switch s {
	case "PENDING":
		return pb.ClaimStatus_PENDING
	case "CLAIMED":
		return pb.ClaimStatus_CLAIMED
	default:
		return pb.ClaimStatus_LOCKED
	}
}

// derivedToolFamily 从槽位推导初始流派多数派；并列时随机选一个。
func derivedToolFamily(s *pb.GameState) pb.Family {
	return derivedToolFamilyAt(s, rand.Uint64())
}

// derivedToolFamilyAt 从槽位推导初始流派多数派；并列时用给定随机值选一个。
// 把随机值作为参数后，在线重建仍可随机，而搜索可使用可复现的公开局面采样。
func derivedToolFamilyAt(s *pb.GameState, tieBreak uint64) pb.Family {
	counts := [5]int{}
	for _, slot := range s.Slots {
		if m := slot.Monster; m != nil && m.Family >= pb.Family_BONE && m.Family <= pb.Family_INSECT {
			counts[m.Family]++
		}
	}
	most, leaders := 0, []pb.Family{}
	for f := pb.Family_BONE; f <= pb.Family_INSECT; f++ {
		if counts[f] > most {
			most, leaders = counts[f], []pb.Family{f}
		} else if most > 0 && counts[f] == most {
			leaders = append(leaders, f)
		}
	}
	if len(leaders) == 0 {
		return pb.Family_FAMILY_UNSPECIFIED
	}
	if len(leaders) == 1 {
		return leaders[0]
	}
	return leaders[tieBreak%uint64(len(leaders))]
}

// buildSim 用当前观察重建一个推演环境（每次调用 RNG 重新随机）。
func buildSim(o *Observation) (*simEnv, error) {
	s, err := RebuildState(o)
	if err != nil {
		return nil, err
	}
	return &simEnv{state: s}, nil
}

// buildSimSample 构造一个由“可见观察 + 样本编号”唯一确定的未知未来。
// 它不读取真实游戏 RNG；不同样本仍覆盖不同未来，但同一局面反复决策、或并行基准
// 调度顺序变化时，候选动作会面对完全相同且可复现的样本集合。
func buildSimSample(o *Observation, sample int) (*simEnv, error) {
	s, err := RebuildState(o)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("可见观察无法生成采样种子: %w", err)
	}
	digest := sha256.Sum256(raw)
	base := binary.LittleEndian.Uint64(digest[:8])
	sampleKey := mixSample64(uint64(sample) ^ 0x9e3779b97f4a7c15)
	s.InitRng = mixSample64(base ^ sampleKey ^ 0x243f6a8885a308d3)
	s.OfferRng = mixSample64(base ^ sampleKey ^ 0x13198a2e03707344)
	s.EffectRng = mixSample64(base ^ sampleKey ^ 0xa4093822299f31d0)
	s.OpeningToolFamily = derivedToolFamilyAt(s, mixSample64(base^sampleKey^0x082efa98ec4e6c89))
	return &simEnv{state: s}, nil
}

// mixSample64 是仅供 AI 公开局面采样使用的 SplitMix64 混合函数。
func mixSample64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// observe 返回推演环境的可见观察（内部复用同一构建逻辑）。
func (e *simEnv) observe() *Observation {
	return FromGameState(e.state)
}

// step 在推演环境中执行动作（槽位目标映射为推演状态的怪物 id）。
func (e *simEnv) step(a *Action) (*Observation, error) {
	if e.state.Offer == nil || e.state.Phase == pb.Phase_FINISHED {
		return nil, fmt.Errorf("推演状态已结束")
	}
	cmd := &pb.Command{Type: a.Type, OfferId: e.state.Offer.Id}
	switch a.Type {
	case "skip_unknown":
	case "refresh":
	case "choose":
		cmd.CardId = a.CardID
		for _, idx := range a.Slots {
			if idx < 0 || int(idx) >= len(e.state.Slots) || e.state.Slots[idx].Monster == nil {
				return nil, fmt.Errorf("槽位 %d 无怪物", idx)
			}
			cmd.TargetIds = append(cmd.TargetIds, e.state.Slots[idx].Monster.Id)
		}
	default:
		return nil, fmt.Errorf("未知动作类型 %q", a.Type)
	}
	next, _, err := engine.Apply(e.state, cmd, rules)
	if err != nil {
		return nil, err
	}
	e.state = next
	return e.observe(), nil
}

// clone 深拷贝推演环境（rollout 分支用）。
func (e *simEnv) clone() *simEnv {
	return &simEnv{state: proto.Clone(e.state).(*pb.GameState)}
}
