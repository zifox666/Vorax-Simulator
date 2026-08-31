package ai

import (
	"fmt"
	"strings"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

type CatalogCard struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Kind        int32  `json:"kind"`
	Rarity      int32  `json:"rarity"`
	BoxSize     int    `json:"boxSize"`
}

type VisibleFlow struct {
	PotionTurns int32 `json:"potionTurns"`
	SchemeTurns int32 `json:"schemeTurns"`
}

var visibleFlow = VisibleFlow{PotionTurns: 8, SchemeTurns: 3}

func PublicCatalog() any {
	cards := []CatalogCard{}
	for _, c := range rules.Cards {
		if !c.Enabled {
			continue
		}
		size := 0
		switch c.Handler {
		case "potion_box_3", "potion_normal_box_3":
			size = 3
		case "potion_box_5", "potion_normal_box_5":
			size = 5
		}
		cards = append(cards, CatalogCard{c.Id, c.Name, c.Description, int32(c.Kind), int32(c.Rarity), size})
	}
	return struct {
		RulesVersion   string                `json:"rulesVersion"`
		ContentVersion string                `json:"contentVersion"`
		Cards          []CatalogCard         `json:"cards"`
		Monsters       []engine.MonsterEntry `json:"monsters"`
		Flow           VisibleFlow           `json:"flow"`
	}{engine.RulesVersion, engine.ContentVersion, cards, engine.MonsterCatalog(), visibleFlow}
}

// VisibleInput accepts OCR fields and session history, never client-authored
// legal targets, random streams, or simulator save tokens.
type VisibleInput struct {
	RulesVersion    string        `json:"rulesVersion"`
	ContentVersion  string        `json:"contentVersion"`
	Phase           string        `json:"phase"`
	BaseCursor      int32         `json:"baseCursor"`
	CompletedTurns  int32         `json:"completedTurns"`
	Score           int64         `json:"score"`
	Slots           []VisibleSlot `json:"slots"`
	Tools           []string      `json:"tools"`
	UnknownTools    int32         `json:"unknownTools,omitempty"` // Acquired but missed by OCR; no effects are invented.
	Offer           OfferView     `json:"offer"`
	CardIDs         []string      `json:"cardIds"`
	PotionRefreshes int32         `json:"potionRefreshes"`
	ToolRefreshes   int32         `json:"toolRefreshes"`
	ToolClaims      []ClaimView   `json:"toolClaims"`
}

type VisibleSlot struct {
	Index        int32  `json:"index"`
	DefinitionID string `json:"definitionId"`
	Activity     int64  `json:"activity"`
	Quantity     int64  `json:"quantity"`
}

func FromVisible(v *VisibleInput) (*Observation, error) {
	bad := func(format string, args ...any) (*Observation, error) {
		return nil, fmt.Errorf("可见数据无效："+format, args...)
	}
	if v == nil {
		return bad("缺少 visible 对象")
	}
	if v.RulesVersion != engine.RulesVersion || v.ContentVersion != engine.ContentVersion {
		return bad("版本不匹配：rulesVersion 应为 %q，收到 %q；contentVersion 应为 %q，收到 %q，请重新获取卡牌目录", engine.RulesVersion, v.RulesVersion, engine.ContentVersion, v.ContentVersion)
	}
	if v.Phase != "CHOOSING" && v.Phase != "FINISHED" {
		return bad("phase 应为 CHOOSING 或 FINISHED，收到 %q；请从第 1 回合的开局用具候选开始", v.Phase)
	}
	var incomplete []string
	if len(v.Slots) != 6 {
		incomplete = append(incomplete, fmt.Sprintf("slots 需要 6 个培养皿，收到 %d 个", len(v.Slots)))
	}
	if len(v.ToolClaims) != 2 {
		incomplete = append(incomplete, fmt.Sprintf("toolClaims 需要 8000、28000 两档领取记录，收到 %d 条", len(v.ToolClaims)))
	}
	if v.PotionRefreshes < 0 || v.PotionRefreshes > 3 {
		incomplete = append(incomplete, fmt.Sprintf("potionRefreshes 药剂刷新次数应为 0–3，收到 %d", v.PotionRefreshes))
	}
	if v.ToolRefreshes < 0 || v.ToolRefreshes > 2 {
		incomplete = append(incomplete, fmt.Sprintf("toolRefreshes 用具刷新次数应为 0–2，收到 %d", v.ToolRefreshes))
	}
	if v.UnknownTools < 0 || v.UnknownTools > 3 {
		incomplete = append(incomplete, fmt.Sprintf("unknownTools 未知用具数量应为 0–3，收到 %d", v.UnknownTools))
	}
	if len(incomplete) > 0 {
		return bad("%s", strings.Join(incomplete, "；"))
	}
	endCursor := 1 + visibleFlow.PotionTurns + visibleFlow.SchemeTurns
	if v.BaseCursor < 0 || v.BaseCursor > endCursor || v.CompletedTurns < 0 || v.CompletedTurns > endCursor+1 {
		return bad("流程进度超出范围：baseCursor 应为 0–%d，收到 %d；completedTurns 应为 0–%d，收到 %d", endCursor, v.BaseCursor, endCursor+1, v.CompletedTurns)
	}
	o := &Observation{Phase: v.Phase, BaseCursor: v.BaseCursor, CompletedTurns: v.CompletedTurns,
		Score: v.Score, Tools: v.Tools, Offer: v.Offer, PotionRefreshes: v.PotionRefreshes, ToolRefreshes: v.ToolRefreshes}
	claimed, pending := int32(0), int64(0)
	for i, cl := range v.ToolClaims {
		if cl.Threshold != []int64{8000, 28000}[i] {
			return bad("toolClaims[%d].threshold 应为 %d，收到 %d", i, []int64{8000, 28000}[i], cl.Threshold)
		}
		if cl.Status != "LOCKED" && cl.Status != "PENDING" && cl.Status != "CLAIMED" {
			return bad("toolClaims[%d].status 应为 LOCKED、PENDING 或 CLAIMED，收到 %q", i, cl.Status)
		}
		if i == 1 && cl.Status != "LOCKED" && v.ToolClaims[0].Status == "LOCKED" {
			return bad("28000 档已解锁，但 toolClaims[0] 的 8000 档仍为 LOCKED")
		}
		if cl.Status == "CLAIMED" {
			if pending != 0 {
				return bad("%d 档已领取，但 %d 档仍待领取", cl.Threshold, pending)
			}
			claimed++
		}
		if cl.Status == "PENDING" && pending == 0 {
			pending = cl.Threshold
		}
	}
	baseTurns := max(v.BaseCursor-1, 0)
	if v.CompletedTurns != baseTurns+claimed {
		return bad("completedTurns 回合记录不一致：基础回合 %d + 已领取奖励用具 %d = %d，收到 %d；可能漏扫用具阶段", baseTurns, claimed, baseTurns+claimed, v.CompletedTurns)
	}
	if want := int(claimed) + min(int(v.BaseCursor), 1); len(v.Tools)+int(v.UnknownTools) != want {
		return bad("tools 已获得用具记录不完整：当前进度应有 %d 件，收到 %d 件（%v），另有未知用具 %d 件；可能漏扫用具阶段", want, len(v.Tools), v.Tools, v.UnknownTools)
	}
	for i, id := range v.Tools {
		c := rules.Card(id)
		if c == nil || !c.Enabled || c.Kind != pb.CardKind_TOOL {
			return bad("tools[%d]=%q 不是目录中已启用的手术用具", i, id)
		}
	}
	monsters := map[string]engine.MonsterEntry{}
	for _, d := range engine.MonsterCatalog() {
		monsters[d.ID] = d
	}
	for i, slot := range v.Slots {
		if slot.Index != int32(i) {
			return bad("第 %d 号培养皿 slots[%d].index 应为 %d，收到 %d", i+1, i, i, slot.Index)
		}
		if slot.Activity < 0 || slot.Quantity < 0 {
			return bad("第 %d 号培养皿属性不能为负数：activity=%d，quantity=%d", i+1, slot.Activity, slot.Quantity)
		}
		sv := SlotView{Index: slot.Index}
		if slot.DefinitionID != "" {
			d, ok := monsters[slot.DefinitionID]
			if !ok {
				return bad("第 %d 号培养皿 slots[%d].definitionId=%q 未收录于怪物目录", i+1, i, slot.DefinitionID)
			}
			if slot.Activity == 0 || slot.Quantity == 0 {
				missing := []string{}
				if slot.Activity == 0 {
					missing = append(missing, "activity（活性）")
				}
				if slot.Quantity == 0 {
					missing = append(missing, "quantity（数量）")
				}
				return bad("第 %d 号培养皿 %s 缺少有效的 %s，收到 0；请重新识别", i+1, d.Name, strings.Join(missing, "、"))
			}
			sv.DefinitionID, sv.Name, sv.Family, sv.Rarity = d.ID, d.Name, d.Family, d.Rarity
			sv.Activity, sv.Quantity = slot.Activity, slot.Quantity
		} else if slot.Activity != 0 || slot.Quantity != 0 {
			return bad("第 %d 号培养皿缺少 definitionId（怪物名称），却有 activity=%d、quantity=%d；不能判为空槽", i+1, slot.Activity, slot.Quantity)
		}
		o.Slots = append(o.Slots, sv)
	}
	if v.Phase == "FINISHED" {
		if v.BaseCursor != endCursor || pending != 0 || len(v.CardIDs) != 0 || v.Offer.Kind != 0 {
			return bad("FINISHED 状态不完整：baseCursor 应为 %d，收到 %d；待领取门槛=%d，剩余候选=%v，offer.kind=%d", endCursor, v.BaseCursor, pending, v.CardIDs, v.Offer.Kind)
		}
	} else {
		kind, threshold := int32(pb.CardKind_POTION), int64(0)
		if pending != 0 {
			kind, threshold = int32(pb.CardKind_TOOL), pending
		} else if v.BaseCursor == 0 {
			kind = int32(pb.CardKind_TOOL)
		} else if v.BaseCursor > visibleFlow.PotionTurns && v.BaseCursor < endCursor {
			kind = int32(pb.CardKind_SCHEME)
		} else if v.BaseCursor == endCursor {
			return bad("已无待选卡牌")
		}
		if v.Offer.Kind != kind || v.Offer.RewardThreshold != threshold {
			return bad("候选阶段与历史不一致：offer.kind 应为 %d，收到 %d；offer.rewardThreshold 应为 %d，收到 %d", kind, v.Offer.Kind, threshold, v.Offer.RewardThreshold)
		}
		if len(v.CardIDs) < 1 || len(v.CardIDs) > 5 {
			return bad("cardIds 候选卡需要 1–5 张，收到 %d 张（%v）", len(v.CardIDs), v.CardIDs)
		}
		seen := map[string]bool{}
		for i, id := range v.CardIDs {
			c := rules.Card(id)
			if c == nil || !c.Enabled {
				return bad("cardIds[%d]=%q 未收录或未启用，请刷新卡牌目录或补充该卡牌", i, id)
			}
			if int32(c.Kind) != kind {
				return bad("cardIds[%d]=%q（%s）类别为 %d，当前阶段要求 %d", i, id, c.Name, c.Kind, kind)
			}
			if seen[id] {
				return bad("cardIds[%d]=%q（%s）在同批候选中重复出现", i, id, c.Name)
			}
			if kind == int32(pb.CardKind_TOOL) && threshold == 0 && c.CoreFamily == 0 {
				return bad("cardIds[%d]=%q（%s）不是开局核心用具", i, id, c.Name)
			}
			seen[id] = true
			o.Cards = append(o.Cards, CardView{ID: id})
		}
	}
	o.Rewards.ToolClaims = v.ToolClaims
	s, err := RebuildState(o)
	if err != nil {
		return nil, err
	}
	if err := engine.RecalculateVisibleRewards(s); err != nil {
		return nil, err
	}
	if s.Score != v.Score {
		return bad("score 总分与六槽活性乘数量之和不一致：收到 %d，按六槽计算为 %d；请等待动画结束后重新识别", v.Score, s.Score)
	}
	return FromGameState(s), nil
}
