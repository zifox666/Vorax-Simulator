// Package ai 提供"有限信息"AI 决策：它只能看到玩家在 UI 上能看到的内容
// （Observation），看不到 seed 与三条 RNG 流。信息边界由类型强制：
// 所有决策函数只接受 *Observation，绝不接收 *pb.GameState 或 RNG。
package ai

import (
	"fmt"

	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

// rules 是共享的公开规则目录（卡牌定义对玩家可见，不属于隐藏信息）。
var rules = engine.DemoRules()

// Observation 是 AI 可见的全部信息，字段与 UI 渲染一一对应。
// 注意：故意不含 seed / initRng / offerRng / effectRng / runId / stateToken。
type Observation struct {
	Phase           string     `json:"phase"`           // PREPARING / CHOOSING / FINISHED
	StageLabel      string     `json:"stageLabel"`      // 界面阶段标题
	BaseCursor      int32      `json:"baseCursor"`      // 基础选择进度 0-11
	CompletedTurns  int32      `json:"completedTurns"`  // 已完成回合数
	Score           int64      `json:"score"`           // 当前分数
	Slots           []SlotView `json:"slots"`           // 六个培养槽
	Tools           []string   `json:"tools"`           // 已拥有用具 id
	ToolNames       []string   `json:"toolNames"`       // 已拥有用具名
	Offer           OfferView  `json:"offer"`           // 当前候选类型
	Cards           []CardView `json:"cards"`           // 当前候选卡
	CanSkip         bool       `json:"canSkip"`         // 可跳过未知器具
	CanRefresh      bool       `json:"canRefresh"`      // 可刷新候选
	PotionRefreshes int32      `json:"potionRefreshes"` // 剩余药剂刷新
	ToolRefreshes   int32      `json:"toolRefreshes"`   // 剩余用具刷新
	Rewards         RewardView `json:"rewards"`         // 奖励面板
}

// SlotView 对应一个培养槽：目标一律用槽位序号表示（UI 点击培养槽）。
type SlotView struct {
	DefinitionID string `json:"definitionId,omitempty"`
	Name         string `json:"name,omitempty"`
	Index        int32  `json:"index"`
	Family       int32  `json:"family"` // 1 骨卫兵 2 异魔 3 觉醒者 4 蛊虫; 0 空
	Rarity       int32  `json:"rarity"` // 1 普通 2 魔法 3 稀有 4 首领
	Activity     int64  `json:"activity"`
	Quantity     int64  `json:"quantity"`
}

// OfferView 描述当前候选的类别（药剂 / 用具 / 方案 / 未知器具）与奖励门槛。
type OfferView struct {
	Kind            int32 `json:"kind"`            // 1 未知 2 药剂 3 用具 4 方案
	RewardThreshold int64 `json:"rewardThreshold"` // 用具奖励门槛；0 为开局用具
}

// CardView 对应一张候选卡：名称、描述、稀有度与全部合法目标（槽位组合）。
type CardView struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Kind        int32     `json:"kind"`
	Rarity      int32     `json:"rarity"` // 药剂稀有度 1-4
	Playable    bool      `json:"playable"`
	TargetSets  [][]int32 `json:"targetSets"` // 合法目标：槽位序号的有序组合
}

// RewardView 对应左侧奖励面板：奖励罐、掉落加成、下一档门槛与用具领取状态。
type RewardView struct {
	Jars             []int32     `json:"jars"`             // 6 个奖励罐颜色
	DropBonusPercent int32       `json:"dropBonusPercent"` // 掉落数量加成
	NextThreshold    int64       `json:"nextThreshold"`    // 下一档门槛
	NextRewardLabel  string      `json:"nextRewardLabel"`  // 下一档奖励描述
	ToolClaims       []ClaimView `json:"toolClaims"`       // 8000/28000 用具领取状态
}

// ClaimView 描述一个分数门槛用具的领取状态（玩家可见的流程状态）。
type ClaimView struct {
	Threshold int64  `json:"threshold"`
	Status    string `json:"status"` // LOCKED / PENDING / CLAIMED
}

// Done 表示对局是否已经结束。
func (o *Observation) Done() bool { return o.Phase == "FINISHED" }

// FromGameState 从真实状态构建 AI 可见观察（只提取 UI 渲染字段，剔除隐藏状态）。
// 这是隐藏信息边界唯一的"出口"：AI 决策代码只能拿到这里的结果。
func FromGameState(s *pb.GameState) *Observation {
	v := engine.View(s, rules)
	o := &Observation{
		Phase:           s.Phase.String(),
		StageLabel:      v.StageLabel,
		BaseCursor:      s.BaseCursor,
		CompletedTurns:  s.CompletedTurns,
		Score:           s.Score,
		CanSkip:         v.CanSkip,
		CanRefresh:      v.CanRefresh,
		PotionRefreshes: s.PotionRefreshes,
		ToolRefreshes:   s.ToolRefreshes,
		Slots:           []SlotView{},
		Tools:           []string{},
		ToolNames:       []string{},
		Cards:           []CardView{},
	}
	for _, slot := range s.Slots {
		sv := SlotView{Index: slot.Index}
		if slot.Monster != nil {
			sv.DefinitionID, sv.Name = slot.Monster.DefinitionId, slot.Monster.Name
			sv.Family, sv.Rarity, sv.Activity, sv.Quantity = int32(slot.Monster.Family), int32(slot.Monster.Rarity), slot.Monster.Activity, slot.Monster.Quantity
		}
		o.Slots = append(o.Slots, sv)
	}
	for _, id := range s.Tools {
		o.Tools = append(o.Tools, id)
		if c := rules.Card(id); c != nil {
			o.ToolNames = append(o.ToolNames, c.Name)
		}
	}
	if s.Offer != nil {
		o.Offer = OfferView{Kind: int32(s.Offer.Kind), RewardThreshold: s.Offer.RewardThreshold}
		for _, cv := range v.Cards {
			card := cv.Definition
			view := CardView{ID: card.Id, Name: card.Name, Description: card.Description,
				Kind: int32(card.Kind), Rarity: int32(card.Rarity), Playable: cv.Playable}
			indexOf := map[string]int32{}
			for _, slot := range s.Slots {
				if slot.Monster != nil {
					indexOf[slot.Monster.Id] = slot.Index
				}
			}
			for _, ts := range cv.LegalTargets {
				row := make([]int32, 0, len(ts.Ids))
				ok := true
				for _, mid := range ts.Ids {
					if idx, found := indexOf[mid]; found {
						row = append(row, idx)
					} else {
						ok = false
						break
					}
				}
				if ok {
					view.TargetSets = append(view.TargetSets, row)
				}
			}
			o.Cards = append(o.Cards, view)
		}
	}
	if s.Rewards != nil {
		o.Rewards = RewardView{
			Jars:             toInt32s(s.Rewards.Jars),
			DropBonusPercent: s.Rewards.DropBonusPercent,
			NextThreshold:    s.Rewards.NextThreshold,
			NextRewardLabel:  s.Rewards.NextRewardLabel,
		}
		for _, cl := range s.Rewards.ToolClaims {
			o.Rewards.ToolClaims = append(o.Rewards.ToolClaims, ClaimView{Threshold: cl.Threshold, Status: cl.Status.String()})
		}
	}
	return o
}

func toInt32s(xs []pb.JarColor) []int32 {
	out := make([]int32, 0, len(xs))
	for _, x := range xs {
		out = append(out, int32(x))
	}
	return out
}

func (o *Observation) String() string {
	return fmt.Sprintf("obs{phase=%s score=%d cursor=%d slots=%d cards=%d}", o.Phase, o.Score, o.BaseCursor, len(o.Slots), len(o.Cards))
}
