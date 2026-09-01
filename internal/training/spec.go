package training

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"
	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

const SpecVersion = "training-v1"

type Spec struct {
	Message     *pb.TrainingSpec
	actions     []*ai.Action
	actionIndex map[string]int
	monsterIdx  map[string]int32
	cardIdx     map[string]int32
	toolIdx     map[string]int32
}

func NewSpec(rules *engine.Rules) (*Spec, error) {
	monsterIDs := make([]string, 0)
	for _, d := range engine.MonsterCatalog() {
		monsterIDs = append(monsterIDs, d.ID)
	}
	sort.Strings(monsterIDs)
	cards := make([]*pb.CardDefinition, 0)
	for _, card := range rules.Cards {
		if card.Enabled {
			cards = append(cards, card)
		}
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Id < cards[j].Id })
	cardIDs, toolIDs := make([]string, 0, len(cards)), []string{}
	for _, card := range cards {
		cardIDs = append(cardIDs, card.Id)
		if card.Kind == pb.CardKind_TOOL {
			toolIDs = append(toolIDs, card.Id)
		}
	}
	actions := []*ai.Action{{Type: "skip_unknown"}, {Type: "refresh"}}
	for _, card := range cards {
		for _, slots := range catalogTargets(card.MinTargets, card.MaxTargets) {
			actions = append(actions, &ai.Action{Type: "choose", CardID: card.Id, Slots: slots})
		}
	}
	msg := &pb.TrainingSpec{
		SpecVersion: SpecVersion, RulesVersion: rules.Version, ContentVersion: rules.ContentVersion,
		RngVersion: engine.RNGVersion, MonsterIds: monsterIDs, CardIds: cardIDs, ToolIds: toolIDs,
		Tensor: &pb.TrainingTensorSpec{SlotCount: 6, CandidateCount: 5, ToolCount: int32(len(toolIDs)), ActionCount: int32(len(actions)), IntegerEncoding: "exact-int64"},
	}
	s := &Spec{Message: msg, actions: actions, actionIndex: map[string]int{}, monsterIdx: oneBased(monsterIDs), cardIdx: oneBased(cardIDs), toolIdx: oneBased(toolIDs)}
	for i, action := range actions {
		s.actionIndex[actionKey(action)] = i
		msg.Actions = append(msg.Actions, &pb.TrainingActionSpec{Index: int32(i), Action: actionMessage(action)})
	}
	b, err := (proto.MarshalOptions{Deterministic: true}).Marshal(msg)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	msg.SpecHash = hex.EncodeToString(sum[:])
	return s, nil
}

func oneBased(ids []string) map[string]int32 {
	m := make(map[string]int32, len(ids))
	for i, id := range ids {
		m[id] = int32(i + 1)
	}
	return m
}

func catalogTargets(minTargets, maxTargets int32) [][]int32 {
	result := [][]int32{}
	var visit func([]int32)
	visit = func(current []int32) {
		if int32(len(current)) >= minTargets {
			result = append(result, append([]int32{}, current...))
		}
		if int32(len(current)) >= maxTargets {
			return
		}
		for slot := int32(0); slot < 6; slot++ {
			seen := false
			for _, existing := range current {
				seen = seen || existing == slot
			}
			if !seen {
				visit(append(append([]int32{}, current...), slot))
			}
		}
	}
	visit(nil)
	return result
}

func actionKey(action *ai.Action) string {
	parts := make([]string, len(action.Slots))
	for i, slot := range action.Slots {
		parts[i] = strconv.Itoa(int(slot))
	}
	return action.Type + "\x00" + action.CardID + "\x00" + strings.Join(parts, ",")
}

func actionMessage(action *ai.Action) *pb.TrainingAction {
	return &pb.TrainingAction{Type: action.Type, CardId: action.CardID, TargetSlots: append([]int32{}, action.Slots...)}
}

func actionFromMessage(action *pb.TrainingAction) *ai.Action {
	if action == nil {
		return nil
	}
	return &ai.Action{Type: action.Type, CardID: action.CardId, Slots: append([]int32{}, action.TargetSlots...)}
}

func (s *Spec) Action(index int32) (*ai.Action, error) {
	if index < 0 || int(index) >= len(s.actions) {
		return nil, fmt.Errorf("INVALID_ACTION: actionIndex 超出训练动作目录")
	}
	a := s.actions[index]
	return &ai.Action{Type: a.Type, CardID: a.CardID, Slots: append([]int32{}, a.Slots...)}, nil
}

func (s *Spec) Encode(o *ai.Observation) (*pb.TrainingObservation, *pb.TensorObservation, []*pb.TrainingAction, []int32) {
	semantic := observationMessage(o)
	tensor := &pb.TensorObservation{
		Phase: phaseIndex(o.Phase), Progress: []int32{o.BaseCursor, o.CompletedTurns}, Score: o.Score,
		SlotMonsters: make([]int32, 6), SlotFamilies: make([]int32, 6), SlotRarities: make([]int32, 6),
		SlotActivities: make([]int64, 6), SlotQuantities: make([]int64, 6), ToolCounts: make([]int32, len(s.Message.ToolIds)),
		Offer: []int32{o.Offer.Kind}, OfferRewardThreshold: o.Offer.RewardThreshold,
		CandidateCards: make([]int32, 5), CandidatePlayable: make([]int32, 5),
		Refreshes: []int32{o.PotionRefreshes, o.ToolRefreshes}, RewardJars: paddedInt32(o.Rewards.Jars, 6),
		DropBonusPercent: o.Rewards.DropBonusPercent, ToolClaimStatuses: make([]int32, 2), NextRewardThreshold: o.Rewards.NextThreshold,
	}
	for _, slot := range o.Slots {
		if slot.Index < 0 || slot.Index >= 6 {
			continue
		}
		i := int(slot.Index)
		tensor.SlotMonsters[i], tensor.SlotFamilies[i], tensor.SlotRarities[i] = s.monsterIdx[slot.DefinitionID], slot.Family, slot.Rarity
		tensor.SlotActivities[i], tensor.SlotQuantities[i] = slot.Activity, slot.Quantity
	}
	for _, tool := range o.Tools {
		if idx := s.toolIdx[tool]; idx > 0 {
			tensor.ToolCounts[idx-1]++
		}
	}
	for i, card := range o.Cards {
		if i >= 5 {
			break
		}
		tensor.CandidateCards[i] = s.cardIdx[card.ID]
		if card.Playable {
			tensor.CandidatePlayable[i] = 1
		}
	}
	for i, claim := range o.Rewards.ToolClaims {
		if i >= 2 {
			break
		}
		tensor.ToolClaimStatuses[i] = map[string]int32{"PENDING": 1, "CLAIMED": 2}[claim.Status]
	}
	legal := ai.LegalActionsFromObservation(o)
	legalMessages := make([]*pb.TrainingAction, 0, len(legal))
	mask := make([]int32, len(s.actions))
	for _, action := range legal {
		if index, ok := s.actionIndex[actionKey(action)]; ok {
			mask[index] = 1
			legalMessages = append(legalMessages, actionMessage(action))
		}
	}
	return semantic, tensor, legalMessages, mask
}

func paddedInt32(source []int32, size int) []int32 {
	result := make([]int32, size)
	copy(result, source)
	return result
}

func phaseIndex(phase string) int32 {
	return map[string]int32{"PREPARING": 1, "CHOOSING": 2, "FINISHED": 3}[phase]
}

func observationMessage(o *ai.Observation) *pb.TrainingObservation {
	m := &pb.TrainingObservation{
		Phase: o.Phase, StageLabel: o.StageLabel, BaseCursor: o.BaseCursor, CompletedTurns: o.CompletedTurns, Score: o.Score,
		Tools: append([]string{}, o.Tools...), ToolNames: append([]string{}, o.ToolNames...),
		Offer:   &pb.TrainingOfferObservation{Kind: o.Offer.Kind, RewardThreshold: o.Offer.RewardThreshold},
		CanSkip: o.CanSkip, CanRefresh: o.CanRefresh, PotionRefreshes: o.PotionRefreshes, ToolRefreshes: o.ToolRefreshes,
		Rewards: &pb.TrainingRewardObservation{Jars: append([]int32{}, o.Rewards.Jars...), DropBonusPercent: o.Rewards.DropBonusPercent, NextThreshold: o.Rewards.NextThreshold, NextRewardLabel: o.Rewards.NextRewardLabel},
	}
	for _, slot := range o.Slots {
		m.Slots = append(m.Slots, &pb.TrainingSlotObservation{DefinitionId: slot.DefinitionID, Name: slot.Name, Index: slot.Index, Family: slot.Family, Rarity: slot.Rarity, Activity: slot.Activity, Quantity: slot.Quantity})
	}
	for _, card := range o.Cards {
		c := &pb.TrainingCardObservation{Id: card.ID, Name: card.Name, Description: card.Description, Kind: card.Kind, Rarity: card.Rarity, Playable: card.Playable}
		for _, targets := range card.TargetSets {
			c.TargetSets = append(c.TargetSets, &pb.TrainingTargetSet{Slots: append([]int32{}, targets...)})
		}
		m.Cards = append(m.Cards, c)
	}
	for _, claim := range o.Rewards.ToolClaims {
		m.Rewards.ToolClaims = append(m.Rewards.ToolClaims, &pb.TrainingClaimObservation{Threshold: claim.Threshold, Status: claim.Status})
	}
	return m
}
