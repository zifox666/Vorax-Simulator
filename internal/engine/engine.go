package engine

import (
	"fmt"
	"sort"

	pb "vorax/internal/protocol"

	"google.golang.org/protobuf/proto"
)

func New(runID, userID, seed string, pet int32, r *Rules) (*pb.GameState, error) {
	if pet < 0 || pet > 2 || seed == "" || len(seed) > 256 {
		return nil, fmt.Errorf("INVALID_INPUT: 种子或宠物刷新次数无效")
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	s := &pb.GameState{RunId: runID, UserId: userID, Revision: 1, FormatVersion: 1, RulesVersion: r.Version, ContentVersion: r.ContentVersion, RngVersion: RNGVersion, Seed: seed, Phase: pb.Phase_PREPARING, PotionRefreshes: 3, ToolRefreshes: pet, InitialPetRefreshes: pet, InitRng: streamSeed(seed, "initialization"), OfferRng: streamSeed(seed, "offers"), EffectRng: streamSeed(seed, "effects"), NextMonsterId: 1, NextOfferId: 1, Rewards: &pb.RewardState{ToolClaims: []*pb.ToolClaim{{Threshold: 8000}, {Threshold: 28000}}}}
	for i := 0; i < 6; i++ {
		s.Slots = append(s.Slots, &pb.Slot{Index: int32(i)})
	}
	if err := makeOffer(s, r, pb.CardKind_UNKNOWN, "preparation", 0); err != nil {
		return nil, err
	}
	return s, updateRewards(s, false)
}

// Apply clones the signed checkpoint. Every failure rolls back RNG, counters,
// events and mutations together; the caller can safely retry the same command.
func Apply(state *pb.GameState, cmd *pb.Command, r *Rules) (*pb.GameState, []*pb.GameEvent, error) {
	if err := ValidateState(state, r); err != nil {
		return nil, nil, err
	}
	if cmd == nil || state.Phase == pb.Phase_FINISHED || state.Offer == nil {
		return nil, nil, fmt.Errorf("INVALID_COMMAND: 当前阶段不能操作")
	}
	if cmd.OfferId != state.Offer.Id {
		return nil, nil, fmt.Errorf("STALE_OFFER: 候选已变化，请恢复最新存档")
	}
	s := proto.Clone(state).(*pb.GameState)
	c := &context{state: s, rules: r, limit: 512}
	s.Revision++
	if cmd.Type == "refresh" {
		if len(cmd.TargetIds) > 0 || cmd.CardId != "" {
			return nil, nil, fmt.Errorf("INVALID_COMMAND: 刷新不接受卡牌或目标")
		}
		switch s.Offer.Kind {
		case pb.CardKind_POTION:
			if s.PotionRefreshes <= 0 {
				return nil, nil, fmt.Errorf("NO_REFRESH: 药剂刷新次数不足")
			}
			s.PotionRefreshes--
		case pb.CardKind_TOOL:
			if s.ToolRefreshes <= 0 {
				return nil, nil, fmt.Errorf("NO_REFRESH: 用具刷新次数不足")
			}
			s.ToolRefreshes--
		default:
			return nil, nil, fmt.Errorf("INVALID_COMMAND: 当前选择不可刷新")
		}
		if err := makeOffer(s, r, s.Offer.Kind, "refresh", s.Offer.RewardThreshold); err != nil {
			return nil, nil, err
		}
		c.emit("refreshed", "已刷新候选，不消耗回合", nil, 0, 0)
		return s, c.events, c.err
	}
	if cmd.Type == "skip_unknown" {
		if s.Phase != pb.Phase_PREPARING || cmd.CardId != "" || len(cmd.TargetIds) > 0 {
			return nil, nil, fmt.Errorf("INVALID_COMMAND: 只能跳过未知器具")
		}
		c.initialize(3, 0, false)
	} else if cmd.Type == "choose" {
		if !contains(s.Offer.CardIds, cmd.CardId) {
			return nil, nil, fmt.Errorf("INVALID_CARD: 卡牌不在当前候选中")
		}
		card := r.Card(cmd.CardId)
		if card == nil || !card.Enabled || card.Kind != s.Offer.Kind {
			return nil, nil, fmt.Errorf("INVALID_CARD: 卡牌无效")
		}
		if card.Kind == pb.CardKind_TOOL && isOpeningToolOffer(s, s.Offer.RewardThreshold) && card.CoreFamily == pb.Family_FAMILY_UNSPECIFIED {
			return nil, nil, fmt.Errorf("INVALID_CARD: 卡牌无效")
		}
		if !validTargets(s, card, cmd.TargetIds) {
			return nil, nil, fmt.Errorf("INVALID_TARGET: 目标不符合卡牌要求")
		}
		c.source = card.Id
		if s.Phase == pb.Phase_PREPARING {
			switch card.Handler {
			case "initial_six":
				c.initialize(6, 0, false)
			case "initial_insects":
				c.initialize(4, pb.Family_INSECT, false)
			case "initial_bones":
				c.initialize(4, pb.Family_BONE, true)
			default:
				return nil, nil, fmt.Errorf("INVALID_CARD: 未知初始化处理器")
			}
		} else {
			if size, _ := potionBox(card); size > 0 {
				if err := c.openPotionBox(card); err != nil {
					return nil, nil, err
				}
				return s, c.events, nil
			}
			kind := s.Offer.Kind
			if kind == pb.CardKind_TOOL {
				if contains(s.Tools, card.Id) {
					return nil, nil, fmt.Errorf("INVALID_CARD: 已拥有此用具")
				}
				s.Tools = append(s.Tools, card.Id)
				for _, claim := range s.Rewards.ToolClaims {
					if claim.Threshold == s.Offer.RewardThreshold {
						claim.Status = pb.ClaimStatus_CLAIMED
					}
				}
				c.emit("tool_acquired", "获得手术用具："+card.Name, nil, 0, 0)
			} else {
				c.play(card, cmd.TargetIds)
			}
			if c.err != nil {
				return nil, nil, c.err
			}
			// Increment before triggers so periodic tools see this formal turn.
			s.CompletedTurns++
			c.emit("turn_end", fmt.Sprintf("第 %d 回合结束结算", s.CompletedTurns), nil, 0, 0)
			if c.err != nil {
				return nil, nil, c.err
			}
			if kind == pb.CardKind_POTION || kind == pb.CardKind_SCHEME || (kind == pb.CardKind_TOOL && s.Offer.RewardThreshold == 0) {
				s.BaseCursor++
			}
			if err := updateRewards(s, true); err != nil {
				return nil, nil, err
			}
			c.emit("scored", fmt.Sprintf("结算分数 %d", s.Score), nil, 0, 0)
		}
	} else {
		return nil, nil, fmt.Errorf("INVALID_COMMAND: 未知命令")
	}
	if c.err != nil {
		return nil, nil, c.err
	}
	if s.Phase == pb.Phase_PREPARING {
		s.Phase = pb.Phase_CHOOSING
		if err := updateRewards(s, false); err != nil {
			return nil, nil, err
		}
	}
	if err := advance(s, r); err != nil {
		return nil, nil, err
	}
	return s, c.events, nil
}

func advance(s *pb.GameState, r *Rules) error {
	for _, claim := range s.Rewards.ToolClaims {
		if claim.Status == pb.ClaimStatus_PENDING {
			return makeOffer(s, r, pb.CardKind_TOOL, "threshold", claim.Threshold)
		}
	}
	if s.BaseCursor >= 11 {
		s.Phase = pb.Phase_FINISHED
		s.Offer = nil
		return nil
	}
	if s.BaseCursor == 0 {
		return makeOffer(s, r, pb.CardKind_TOOL, "opening", 0)
	}
	if s.BaseCursor < 8 {
		return makeOffer(s, r, pb.CardKind_POTION, "base", 0)
	}
	return makeOffer(s, r, pb.CardKind_SCHEME, "base", 0)
}

func offerID(s *pb.GameState) string {
	id := fmt.Sprintf("offer-%d", s.NextOfferId)
	s.NextOfferId++
	return id
}

func makeOffer(s *pb.GameState, r *Rules, kind pb.CardKind, source string, threshold int64) error {
	opening := kind == pb.CardKind_TOOL && isOpeningToolOffer(s, threshold)
	pool := []*pb.CardDefinition{}
	for _, card := range r.Cards {
		if opening && card.CoreFamily == pb.Family_FAMILY_UNSPECIFIED {
			continue
		}
		if card.Enabled && card.Kind == kind && !(kind == pb.CardKind_TOOL && contains(s.Tools, card.Id)) && (kind != pb.CardKind_POTION || len(LegalTargets(s, card)) > 0) {
			pool = append(pool, card)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Id < pool[j].Id })
	count := 3
	if kind == pb.CardKind_UNKNOWN {
		count = 1
	}
	if len(pool) < count {
		return fmt.Errorf("INVALID_CONTENT: 可用候选不足")
	}
	ids := []string{}
	if kind == pb.CardKind_POTION {
		var err error
		ids, err = drawPotionCards(s, r, count, [4]int{40, 35, 20, 5}, 1)
		if err != nil {
			return err
		}
	} else if kind == pb.CardKind_SCHEME {
		for _, card := range pool {
			ids = append(ids, card.Id)
		}
	} else {
		for i := 0; i < count; i++ {
			var j int
			if opening {
				weights, total := openingToolWeights(pool, s.OpeningToolFamily)
				j = toolIndexAt(randomN(&s.OfferRng, total), weights)
			} else {
				j = randomN(&s.OfferRng, len(pool))
			}
			ids = append(ids, pool[j].Id)
			pool = append(pool[:j], pool[j+1:]...)
		}
	}
	s.Offer = &pb.Offer{Id: offerID(s), Kind: kind, CardIds: ids, Source: source, RewardThreshold: threshold}
	return nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
func monsterIDs(s *pb.GameState) []string {
	ids := []string{}
	for _, slot := range s.Slots {
		if slot.Monster != nil {
			ids = append(ids, slot.Monster.Id)
		}
	}
	return ids
}
func getMonster(s *pb.GameState, id string) *pb.Monster {
	for _, slot := range s.Slots {
		if slot.Monster != nil && slot.Monster.Id == id {
			return slot.Monster
		}
	}
	return nil
}

func validTargets(s *pb.GameState, card *pb.CardDefinition, ids []string) bool {
	if card == nil || !card.Enabled {
		return false
	}
	if len(ids) < int(card.MinTargets) || len(ids) > int(card.MaxTargets) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] || getMonster(s, id) == nil {
			return false
		}
		seen[id] = true
	}
	return potionTargetsValid(s, card, ids)
}

// At most six slots: enumerating legal tuples keeps domain decisions entirely
// on the server, including ordered target pairs for devouring.
func LegalTargets(s *pb.GameState, card *pb.CardDefinition) []*pb.TargetSet {
	result := []*pb.TargetSet{}
	ids := monsterIDs(s)
	var visit func([]string)
	visit = func(p []string) {
		if validTargets(s, card, p) {
			result = append(result, &pb.TargetSet{Ids: append([]string{}, p...)})
		}
		if len(p) >= int(card.MaxTargets) {
			return
		}
		for _, id := range ids {
			if !contains(p, id) {
				visit(append(append([]string{}, p...), id))
			}
		}
	}
	visit(nil)
	return result
}

func View(s *pb.GameState, r *Rules) *pb.GameView {
	v := &pb.GameView{State: proto.Clone(s).(*pb.GameState), CanSkip: s.Phase == pb.Phase_PREPARING, Thresholds: append([]int64{}, Thresholds...)}
	if s.Offer != nil {
		for _, id := range s.Offer.CardIds {
			card := r.Card(id)
			if card == nil {
				continue
			}
			targets := LegalTargets(s, card)
			v.Cards = append(v.Cards, &pb.CardView{Definition: card, LegalTargets: targets, Playable: len(targets) > 0})
		}
		v.CanRefresh = (s.Offer.Kind == pb.CardKind_POTION && s.PotionRefreshes > 0) || (s.Offer.Kind == pb.CardKind_TOOL && s.ToolRefreshes > 0)
		switch s.Offer.Kind {
		case pb.CardKind_UNKNOWN:
			v.StageLabel = "手术准备"
		case pb.CardKind_POTION:
			v.StageLabel = fmt.Sprintf("药剂选择 %d / 7", s.BaseCursor)
		case pb.CardKind_SCHEME:
			v.StageLabel = fmt.Sprintf("手术方案 %d / 3", s.BaseCursor-7)
		case pb.CardKind_TOOL:
			if s.Offer.RewardThreshold == 0 {
				v.StageLabel = "开局手术用具"
			} else {
				v.StageLabel = fmt.Sprintf("%d 分奖励 · 手术用具", s.Offer.RewardThreshold)
			}
		}
	} else {
		v.StageLabel = "手术准备完成"
	}
	for _, id := range s.Tools {
		v.Tools = append(v.Tools, r.Card(id))
	}
	return v
}

func ValidateState(s *pb.GameState, r *Rules) error {
	if s == nil || s.FormatVersion != 1 || s.RulesVersion != r.Version || s.ContentVersion != r.ContentVersion || s.RngVersion != RNGVersion {
		return fmt.Errorf("VERSION_UNAVAILABLE: 存档版本不可用，请重新开始一局游戏")
	}
	if len(s.Slots) != 6 || s.Rewards == nil || len(s.Rewards.ToolClaims) != 2 || s.BaseCursor < 0 || s.BaseCursor > 11 || s.CompletedTurns < 0 || s.CompletedTurns > 13 || s.Revision == 0 {
		return fmt.Errorf("INVALID_STATE: 存档结构无效")
	}
	if s.OpeningToolFamily < pb.Family_FAMILY_UNSPECIFIED || s.OpeningToolFamily > pb.Family_INSECT || (s.Phase != pb.Phase_PREPARING && s.OpeningToolFamily == pb.Family_FAMILY_UNSPECIFIED) {
		return fmt.Errorf("INVALID_STATE: 初始流派无效")
	}
	seen := map[string]bool{}
	for i, slot := range s.Slots {
		if slot == nil || slot.Index != int32(i) {
			return fmt.Errorf("INVALID_STATE: 槽位无效")
		}
		if m := slot.Monster; m != nil {
			if m.Id == "" || seen[m.Id] || m.Family < 1 || m.Family > 4 || m.Rarity < 1 || m.Rarity > 4 {
				return fmt.Errorf("INVALID_STATE: 怪物无效")
			}
			seen[m.Id] = true
			if _, err := contribution(m); err != nil {
				return err
			}
		}
	}
	for _, id := range s.Tools {
		if r.Card(id) == nil || r.Card(id).Kind != pb.CardKind_TOOL {
			return fmt.Errorf("INVALID_STATE: 用具无效")
		}
	}
	return nil
}
