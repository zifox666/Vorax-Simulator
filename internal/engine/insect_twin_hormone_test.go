package engine

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func TestInsectTwinHormoneDefinitionAndTargets(t *testing.T) {
	s, r := fixture(t)
	card := r.Card("insect_twin_hormone")
	if card == nil || card.Name != "孪生激素-蛊虫" || card.Rarity != pb.PotionRarity_WHITE || !card.Enabled || card.MinTargets != 1 || card.MaxTargets != 1 {
		t.Fatalf("invalid definition: %v", card)
	}
	if len(LegalTargets(s, card)) != 0 {
		t.Fatal("empty board has legal targets")
	}
	a := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
	b := put(s, 2, pb.Family_INSECT, pb.MonsterRarity_BOSS, 300, 1)
	if len(LegalTargets(s, card)) != 2 || !validTargets(s, card, []string{a}) || !validTargets(s, card, []string{b}) {
		t.Fatal("occupied single target rejected")
	}
	for _, ids := range [][]string{nil, {a, b}, {a, a}, {"missing"}} {
		if validTargets(s, card, ids) {
			t.Fatalf("invalid targets accepted: %v", ids)
		}
	}
	for seed := uint64(0); seed < 64; seed++ {
		s.OfferRng = seed
		ids, err := drawPotionCards(s, r, 3, [4]int{1, 0, 0, 0}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if contains(ids, card.Id) {
			return
		}
	}
	t.Fatal("card was not drawn from white potion pool")
}

func TestInsectTwinHormoneCopiesMutatedDefinition(t *testing.T) {
	seen := map[string]bool{}
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		for seed := uint64(0); seed < 64; seed++ {
			s, r := fixture(t)
			s.EffectRng = seed
			id := put(s, 3, family, pb.MonsterRarity_BOSS, 100, 200)
			before := proto.Clone(s).(*pb.GameState)
			c := potionTestPlay(t, s, r, "insect_twin_hormone", id)
			m, added := getMonster(s, id), s.Slots[0].Monster
			assertNamedMonster(t, m)
			a, q := base(m.Rarity)
			if m.Family != pb.Family_INSECT || m.Activity != 100+a || m.Quantity != 200+q || len(monsterIDs(s)) != 2 {
				t.Fatalf("incorrect mutation: %v", m)
			}
			if added == nil || added.Id == id || added.DefinitionId != m.DefinitionId || added.Name != m.Name || added.Family != m.Family || added.Rarity != m.Rarity || added.Activity != a || added.Quantity != q {
				t.Fatalf("incorrect same-name addition: %v", added)
			}
			if len(c.events) != 2 || c.events[0].Kind != "mutated" || c.events[0].SlotsAfter[0].Monster != nil || c.events[1].Kind != "added" || !proto.Equal(c.events[1].SlotsAfter[0].Monster, added) {
				t.Fatal("incorrect event order or snapshot")
			}
			replay := potionTestPlay(t, before, r, "insect_twin_hormone", id)
			if !proto.Equal(s, before) || !proto.Equal(c.events[0], replay.events[0]) || !proto.Equal(c.events[1], replay.events[1]) {
				t.Fatal("replay mismatch")
			}
			seen[m.DefinitionId] = true
		}
	}
	if len(seen) != 8 {
		t.Fatalf("missing insect variants: %v", seen)
	}
}

func TestInsectTwinHormoneOverflowAndAddedTrigger(t *testing.T) {
	s, r := fixture(t)
	for i := 0; i < 6; i++ {
		put(s, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1000, 1000)
	}
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 200)
	s.Tools = []string{"nail"}
	nextID := s.NextMonsterId
	c := potionTestPlay(t, s, r, "insect_twin_hormone", id)
	m := getMonster(s, id)
	a, q := base(m.Rarity)
	if m.Family != pb.Family_INSECT || m.Activity != 100+a+25 || m.Quantity != 200+q+25 || len(monsterIDs(s)) != 6 || s.NextMonsterId != nextID || eventCount(c.events, "mutated") != 1 || eventCount(c.events, "overflow") != 1 || eventCount(c.events, "added") != 0 {
		t.Fatalf("incorrect full-board result: %v", m)
	}
	s, r = fixture(t)
	id = put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 200)
	put(s, 3, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
	s.Tools = []string{"hatching_egg"}
	c = potionTestPlay(t, s, r, "insect_twin_hormone", id)
	m = getMonster(s, id)
	added := s.Slots[1].Monster
	a, q = base(m.Rarity)
	if added == nil || added.DefinitionId != m.DefinitionId || added.Activity != a || added.Quantity != q+45 || m.Quantity != 200+q+45 || s.Slots[3].Monster.Quantity != 81 || eventCount(c.events, "added") != 1 {
		t.Fatal("same-name addition did not trigger hatching egg")
	}
}

func TestInsectTwinHormoneRollback(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, math.MaxInt64, 1)
	s.Offer.CardIds = []string{"insect_twin_hormone"}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "insect_twin_hormone", TargetIds: []string{id}}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("failed mutation did not roll back atomically")
	}
}
