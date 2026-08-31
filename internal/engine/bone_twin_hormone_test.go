package engine

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func TestBoneTwinHormoneDefinitionAndTargets(t *testing.T) {
	s, r := fixture(t)
	card := r.Card("bone_twin_hormone")
	if card == nil || card.Name != "孪生激素-骨卫兵" || card.Kind != pb.CardKind_POTION || card.Rarity != pb.PotionRarity_WHITE || !card.Enabled || card.MinTargets != 1 || card.MaxTargets != 1 {
		t.Fatalf("invalid card definition: %v", card)
	}
	if len(LegalTargets(s, card)) != 0 {
		t.Fatal("empty board has legal targets")
	}
	a := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	b := put(s, 3, pb.Family_INSECT, pb.MonsterRarity_BOSS, 30, 40)
	if len(LegalTargets(s, card)) != 2 || !validTargets(s, card, []string{a}) || !validTargets(s, card, []string{b}) {
		t.Fatal("single occupied slot must be a legal target")
	}
	for _, ids := range [][]string{nil, {a, b}, {a, a}, {"missing"}} {
		if validTargets(s, card, ids) {
			t.Fatalf("invalid targets accepted: %v", ids)
		}
	}
}

func TestBoneTwinHormoneMutationThenQuantity(t *testing.T) {
	seen := map[pb.MonsterRarity]bool{}
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		for seed := uint64(0); seed < 64; seed++ {
			s, r := fixture(t)
			s.EffectRng = seed
			id := put(s, 0, family, pb.MonsterRarity_BOSS, 100, 200)
			put(s, 3, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 10, 20)
			other := proto.Clone(s.Slots[3].Monster)
			before := proto.Clone(s).(*pb.GameState)
			c := potionTestPlay(t, s, r, "bone_twin_hormone", id)
			m := getMonster(s, id)
			assertNamedMonster(t, m)
			a, q := base(m.Rarity)
			if m.Family != pb.Family_BONE || m.Activity != 100+a || m.Quantity != 200+q+62 || !proto.Equal(s.Slots[3].Monster, other) {
				t.Fatalf("incorrect result: %v", m)
			}
			if len(c.events) != 2 || c.events[0].Kind != "mutated" || c.events[1].Kind != "stats_changed" || c.events[1].ActivityDelta != 0 || c.events[1].QuantityDelta != 62 {
				t.Fatalf("incorrect effect order: %v", c.events)
			}
			if c.events[0].SlotsAfter[0].Monster.Quantity != 200+q || c.events[1].SlotsAfter[0].Monster.Quantity != m.Quantity {
				t.Fatal("quantity bonus applied before mutation")
			}
			replayed := potionTestPlay(t, before, r, "bone_twin_hormone", id)
			if !proto.Equal(s, before) || !proto.Equal(c.events[0], replayed.events[0]) || !proto.Equal(c.events[1], replayed.events[1]) {
				t.Fatal("replay mismatch")
			}
			seen[m.Rarity] = true
		}
	}
	if len(seen) != 4 {
		t.Fatalf("mutation did not cover all rarities: %v", seen)
	}
}

func TestBoneTwinHormoneInWhitePotionPool(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
	for seed := uint64(0); seed < 64; seed++ {
		s.OfferRng = seed
		ids, err := drawPotionCards(s, r, 3, [4]int{1, 0, 0, 0}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if contains(ids, "bone_twin_hormone") {
			return
		}
	}
	t.Fatal("bone twin hormone was not drawn from white potion pool")
}
