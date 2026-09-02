package engine

import (
	"fmt"

	pb "vorax/internal/protocol"
)

// RulesVersion and ContentVersion identify the initial public data snapshot.
// Change either value only when publishing a new, immutable rules or content set.
const RulesVersion = "rules-v3"
const ContentVersion = "cards-v4"

// RNGVersion identifies the random-draw semantics. Bump it whenever draw
// behavior changes (e.g. distribution or consumption), so old saves fail
// validation instead of replaying differently than the player saw.
const RNGVersion = "splitmix64-v5"

type Rules struct {
	Version        string               `json:"version"`
	ContentVersion string               `json:"contentVersion"`
	Cards          []*pb.CardDefinition `json:"cards"`
}

func (r *Rules) Card(id string) *pb.CardDefinition {
	for _, c := range r.Cards {
		if c.Id == id {
			return c
		}
	}
	return nil
}

func DemoRules() *Rules {
	r := &Rules{Version: RulesVersion, ContentVersion: ContentVersion}
	r.Cards = append(r.Cards,
		&pb.CardDefinition{Id: "unknown_six", Name: "截肢标本", Description: "初始怪物改为随机 6 组怪物。", Kind: pb.CardKind_UNKNOWN, Handler: "initial_six", Enabled: true},
		&pb.CardDefinition{Id: "unknown_insects", Name: "虫虫虫虫", Description: "初始怪物改为 4 组蛊虫。", Kind: pb.CardKind_UNKNOWN, Handler: "initial_insects", Enabled: true},
		&pb.CardDefinition{Id: "unknown_bones", Name: "蒸骨坩埚", Description: "初始怪物改为 2 组骨卫兵与其他2组怪物。", Kind: pb.CardKind_UNKNOWN, Handler: "initial_bones", Enabled: true},
		&pb.CardDefinition{Id: "unknown_rares", Name: "稀有怪堆", Description: "初始怪物改为 4 组各不相同种群的稀有怪物。", Kind: pb.CardKind_UNKNOWN, Handler: "initial_rares", Enabled: true},
	)
	r.Cards = append(r.Cards, potionCards()...)
	r.Cards = append(r.Cards, toolCards()...)
	for i, rarity := range []pb.PotionRarity{pb.PotionRarity_WHITE, pb.PotionRarity_BLUE, pb.PotionRarity_GOLD} {
		r.Cards = append(r.Cards, &pb.CardDefinition{
			Id: fmt.Sprintf("scheme_%d", i), Name: []string{"普通手术方案", "魔法手术方案", "稀有手术方案"}[i],
			Description: "选择后结束回合，触发回合结束效果。", Kind: pb.CardKind_SCHEME, Rarity: rarity, Enabled: true,
		})
	}
	return r
}

func (r *Rules) Validate() error {
	if r.Version != RulesVersion || r.ContentVersion != ContentVersion {
		return fmt.Errorf("VERSION_UNAVAILABLE: 不支持的规则或内容版本")
	}
	seen := map[string]bool{}
	known := map[string]bool{"": true, "initial_six": true, "initial_insects": true, "initial_bones": true, "initial_rares": true}
	for _, card := range append(potionCards(), toolCards()...) {
		known[card.Handler] = true
	}
	ops := map[string]bool{"add": true, "buff": true, "mutate": true, "awaken": true, "remove_left": true}
	counts := map[pb.CardKind]int{}
	rarities := map[pb.PotionRarity]int{}
	coreCount := 0
	for _, c := range r.Cards {
		if c == nil || c.Id == "" || seen[c.Id] || !known[c.Handler] || c.MinTargets < 0 || c.MaxTargets < c.MinTargets || c.MaxTargets > 6 {
			return fmt.Errorf("invalid card definition")
		}
		seen[c.Id] = true
		if c.CoreFamily < pb.Family_FAMILY_UNSPECIFIED || c.CoreFamily > pb.Family_INSECT || (c.CoreFamily != pb.Family_FAMILY_UNSPECIFIED && c.Kind != pb.CardKind_TOOL) {
			return fmt.Errorf("invalid core family: %s", c.Id)
		}
		for _, e := range c.Effects {
			if e == nil || !ops[e.Op] || e.Activity < 0 || e.Quantity < 0 {
				return fmt.Errorf("invalid effect: %s", c.Id)
			}
		}
		if c.Enabled {
			counts[c.Kind]++
			if c.CoreFamily != pb.Family_FAMILY_UNSPECIFIED {
				coreCount++
			}
			if c.Kind == pb.CardKind_POTION {
				if c.Rarity < pb.PotionRarity_WHITE || c.Rarity > pb.PotionRarity_RED {
					return fmt.Errorf("invalid potion rarity: %s", c.Id)
				}
				rarities[c.Rarity]++
			}
		}
	}
	if counts[pb.CardKind_UNKNOWN] < 1 || counts[pb.CardKind_TOOL] < 3 || coreCount < 3 || counts[pb.CardKind_SCHEME] != 3 {
		return fmt.Errorf("incomplete card pool")
	}
	for _, x := range []pb.PotionRarity{1, 2, 3, 4} {
		if rarities[x] == 0 {
			return fmt.Errorf("missing potion rarity %v", x)
		}
	}
	return nil
}
