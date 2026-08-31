package engine

import (
	"fmt"
	"sort"

	pb "vorax/internal/protocol"
)

func potionBox(card *pb.CardDefinition) (int, bool) {
	if card == nil || card.Kind != pb.CardKind_POTION {
		return 0, false
	}
	switch card.Handler {
	case "potion_normal_box_3":
		return 3, false
	case "potion_normal_box_5":
		return 5, false
	case "potion_box_3":
		return 3, true
	case "potion_box_5":
		return 5, true
	}
	return 0, false
}

func potionRarityAt(roll int, weights [4]int) pb.PotionRarity {
	for i, weight := range weights {
		if roll < weight {
			return pb.PotionRarity(i + 1)
		}
		roll -= weight
	}
	return pb.PotionRarity_POTION_RARITY_UNSPECIFIED
}

func drawPotionCards(s *pb.GameState, r *Rules, count int, weights [4]int, boxLimit int) ([]string, error) {
	total := 0
	for _, weight := range weights {
		if weight < 0 {
			return nil, fmt.Errorf("INVALID_CONTENT: 抽取权重无效")
		}
		total += weight
	}
	if total == 0 || count < 1 || count > 5 || boxLimit < 0 || boxLimit > 1 {
		return nil, fmt.Errorf("INVALID_CONTENT: 抽取参数无效")
	}
	pool := []*pb.CardDefinition{}
	for _, card := range r.Cards {
		if card.Enabled && card.Kind == pb.CardKind_POTION && len(LegalTargets(s, card)) > 0 {
			pool = append(pool, card)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Id < pool[j].Id })
	ids := make([]string, 0, count)
	for len(ids) < count {
		rarity := potionRarityAt(randomN(&s.OfferRng, total), weights)
		group := []*pb.CardDefinition{}
		for _, card := range pool {
			size, _ := potionBox(card)
			if card.Rarity == rarity && (size == 0 || boxLimit > 0) {
				group = append(group, card)
			}
		}
		if len(group) == 0 {
			return nil, fmt.Errorf("INVALID_CONTENT: 稀有度卡池为空")
		}
		card := group[randomN(&s.OfferRng, len(group))]
		ids = append(ids, card.Id)
		if size, _ := potionBox(card); size != 0 {
			boxLimit--
		}
	}
	return ids, nil
}

func (c *context) openPotionBox(card *pb.CardDefinition) error {
	size, rare := potionBox(card)
	if size == 0 {
		return fmt.Errorf("INVALID_CARD: 药剂箱无效")
	}
	weights := [4]int{40, 35, 20, 5}
	if rare {
		weights = [4]int{30, 20, 40, 10}
	}
	ids, err := drawPotionCards(c.state, c.rules, size, weights, 0)
	if err != nil {
		return err
	}
	c.state.Offer = &pb.Offer{Id: offerID(c.state), Kind: pb.CardKind_POTION, CardIds: ids, Source: "box:" + card.Id}
	c.emit("box_opened", fmt.Sprintf("打开%s，获得%d支药剂候选", card.Name, size), nil, 0, 0)
	return c.err
}
