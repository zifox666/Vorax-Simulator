package engine

import (
	"math"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func potionTestPlay(t *testing.T, s *pb.GameState, r *Rules, id string, ids ...string) *context {
	t.Helper()
	card := r.Card(id)
	if !validTargets(s, card, ids) {
		t.Fatalf("invalid targets for %s: %v", id, ids)
	}
	c := &context{state: s, rules: r, source: id, limit: 512}
	c.play(card, ids)
	if c.err != nil {
		t.Fatal(c.err)
	}
	return c
}

func TestPotionCatalogComplete(t *testing.T) {
	cards := potionCards()
	if len(cards) != 41 {
		t.Fatalf("got %d potion definitions", len(cards))
	}
	ids, names := map[string]bool{}, map[string]bool{}
	for _, card := range cards {
		if card.Kind != pb.CardKind_POTION || ids[card.Id] || names[card.Name] || card.Description == "" {
			t.Fatalf("invalid definition: %v", card)
		}
		if !card.Enabled {
			t.Fatalf("unexpected availability: %s", card.Id)
		}
		ids[card.Id], names[card.Name] = true, true
	}
	for _, id := range []string{"insect_powder", "bone_powder", "awaker_fluid", "fiend_fluid", "awakening", "digestive", "mutation", "fusion", "insect_boost", "lure", "holy_water", "waking_salts", "box_3", "box_5", "normal_box_3", "normal_box_5"} {
		if !ids[id] {
			t.Fatalf("missing %s", id)
		}
	}
}

func TestPotionWakingSaltsBuffsExactlyOneRandomMonster(t *testing.T) {
	s, r := fixture(t)
	if validTargets(s, r.Card("waking_salts"), nil) {
		t.Fatal("waking salts is playable without monsters")
	}
	ids := []string{
		put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20),
		put(s, 2, pb.Family_FIEND, pb.MonsterRarity_MAGIC, 30, 40),
		put(s, 5, pb.Family_INSECT, pb.MonsterRarity_RARE, 50, 60),
	}
	before := proto.Clone(s).(*pb.GameState)
	c := potionTestPlay(t, s, r, "waking_salts")
	d := potionTestPlay(t, before, r, "waking_salts")
	if !proto.Equal(c.state, d.state) || len(c.events) != len(d.events) {
		t.Fatal("waking salts random choice is not replayable")
	}
	changed := 0
	for i, id := range ids {
		m := getMonster(s, id)
		wantActivity, wantQuantity := int64(10+20*i), int64(20+20*i)
		if m.Activity == wantActivity+111 && m.Quantity == wantQuantity+111 {
			changed++
		} else if m.Activity != wantActivity || m.Quantity != wantQuantity {
			t.Fatalf("unexpected waking salts stats: %v", m)
		}
	}
	if changed != 1 || eventCount(c.events, "stats_changed") != 1 {
		t.Fatalf("waking salts changed %d monsters", changed)
	}
}

func potionTestBox(id string) bool {
	switch id {
	case "box_3", "box_5", "normal_box_3", "normal_box_5":
		return true
	}
	return false
}

func TestPotionEnabledCatalogExecutes(t *testing.T) {
	for _, card := range potionCards() {
		if !card.Enabled || potionTestBox(card.Id) {
			continue
		}
		t.Run(card.Id, func(t *testing.T) {
			s, r := fixture(t)
			for i := 0; i < 6; i++ {
				put(s, i, pb.Family(1+i%4), pb.MonsterRarity(1+i%4), 10, 20)
			}
			targets := LegalTargets(s, card)
			if len(targets) == 0 {
				t.Fatal("no legal targets")
			}
			potionTestPlay(t, s, r, card.Id, targets[0].Ids...)
		})
	}
}

func TestPotionDisabledCatalogRejected(t *testing.T) {
	for _, card := range potionCards() {
		t.Run(card.Id, func(t *testing.T) {
			s, r := fixture(t)
			r.Card(card.Id).Enabled = false
			id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
			s.Offer.CardIds = []string{card.Id}
			ids := []string{}
			if card.MinTargets > 0 {
				ids = append(ids, id)
			}
			before := proto.Clone(s)
			next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: card.Id, TargetIds: ids}, r)
			if err == nil || next != nil || events != nil || !proto.Equal(s, before) || len(LegalTargets(s, r.Card(card.Id))) != 0 {
				t.Fatal("disabled potion accepted or mutated state")
			}
		})
	}
}

func TestPotionBoneRemovalBuffs(t *testing.T) {
	for _, card := range []string{"bone_ointment", "peat_dressing"} {
		t.Run(card, func(t *testing.T) {
			s, r := fixture(t)
			if validTargets(s, r.Card(card), nil) {
				t.Fatal("bone prerequisite missing")
			}
			bone := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
			for i := 1; i < 6; i++ {
				put(s, i, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 10, 20)
			}
			c := potionTestPlay(t, s, r, card)
			removed, delta := 3, int64(84)
			if card == "peat_dressing" {
				removed, delta = 5, 100
			}
			m := getMonster(s, bone)
			if eventCount(c.events, "removed") != removed || m.Activity != 10+delta || m.Quantity != 20+delta || len(monsterIDs(s)) != 6-removed {
				t.Fatalf("unexpected removal: %v", s.Slots)
			}
		})
	}
}

func TestPotionBoneOintmentUsesAvailableTargets(t *testing.T) {
	s, r := fixture(t)
	bone := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	put(s, 4, pb.Family_INSECT, pb.MonsterRarity_RARE, 100, 2)
	c := potionTestPlay(t, s, r, "bone_ointment")
	if eventCount(c.events, "removed") != 1 || getMonster(s, bone).Activity != 38 || getMonster(s, bone).Quantity != 48 {
		t.Fatal("wrong available target count")
	}
}

func TestPotionAlienHormones(t *testing.T) {
	for _, card := range []string{"alien_hormone", "targeted_alien_hormone"} {
		t.Run(card, func(t *testing.T) {
			s, r := fixture(t)
			id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 7, 8)
			c := potionTestPlay(t, s, r, card, id)
			want := 4
			if card == "targeted_alien_hormone" {
				want = 2
			}
			if getMonster(s, id) != nil || len(monsterIDs(s)) != want || eventCount(c.events, "added") != want || eventCount(c.events, "removed") != 1 {
				t.Fatal("wrong replacement count")
			}
			for _, slot := range s.Slots {
				if m := slot.Monster; m != nil {
					a, q := base(m.Rarity)
					if card == "targeted_alien_hormone" {
						q += 25
						if m.Rarity != pb.MonsterRarity_MAGIC {
							t.Fatal("wrong targeted rarity")
						}
					} else {
						a += 5
					}
					if m.Activity != a || m.Quantity != q {
						t.Fatalf("wrong replacement stats: %v", m)
					}
				}
			}
		})
	}
}

func TestPotionCleansingSkipsEmptyRightSlots(t *testing.T) {
	for _, family := range []pb.Family{pb.Family_BONE, pb.Family_FIEND} {
		s, r := fixture(t)
		id := put(s, 1, family, pb.MonsterRarity_NORMAL, 10, 20)
		left := put(s, 0, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 1)
		right := put(s, 5, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 1)
		c := potionTestPlay(t, s, r, "cleansing_ointment", id)
		m := getMonster(s, id)
		if m.Activity != 30 || getMonster(s, left) == nil {
			t.Fatal("wrong cleansing effect")
		}
		if family == pb.Family_BONE {
			if m.Quantity != 61 || getMonster(s, right) != nil || eventCount(c.events, "removed") != 1 {
				t.Fatal("missing bone effect")
			}
		} else if m.Quantity != 20 || getMonster(s, right) == nil || eventCount(c.events, "removed") != 0 {
			t.Fatal("non-bone received bone effect")
		}
	}
}

func TestPotionStickyBileTransformsRight(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_AWAKENER, pb.MonsterRarity_RARE, 10, 20)
	right := put(s, 4, pb.Family_BONE, pb.MonsterRarity_NORMAL, 7, 8)
	c := potionTestPlay(t, s, r, "sticky_bile", id)
	m, n := getMonster(s, id), getMonster(s, right)
	if m.Activity != 41 || m.Quantity != 20 || n.Family != m.Family || n.Rarity != m.Rarity || n.Activity != 53 || n.Quantity != 20 || eventCount(c.events, "mutated") != 1 {
		t.Fatalf("wrong bile effect: %v %v", m, n)
	}
}

func TestPotionPetrifiedMarrow(t *testing.T) {
	for _, family := range []pb.Family{pb.Family_BONE, pb.Family_FIEND} {
		s, r := fixture(t)
		id := put(s, 0, family, pb.MonsterRarity_NORMAL, 10, 20)
		c := potionTestPlay(t, s, r, "petrified_marrow", id)
		m := getMonster(s, id)
		if family == pb.Family_FIEND {
			if m.Activity != 30 || m.Quantity != 20 || eventCount(c.events, "mutated") != 0 {
				t.Fatal("fiend mutated again")
			}
		} else {
			a, q := base(m.Rarity)
			if m.Family != pb.Family_FIEND || m.Activity != 60+a || m.Quantity != 20+q || eventCount(c.events, "mutated") != 1 {
				t.Fatal("non-fiend mutation incorrect")
			}
		}
	}
}

func TestPotionPiaMaterMultipliers(t *testing.T) {
	for _, family := range []pb.Family{pb.Family_BONE, pb.Family_AWAKENER} {
		for rarity := pb.MonsterRarity_NORMAL; rarity <= pb.MonsterRarity_BOSS; rarity++ {
			s, r := fixture(t)
			id := put(s, 0, family, rarity, 10, 20)
			c := potionTestPlay(t, s, r, "pia_mater", id)
			count := 1
			if family == pb.Family_AWAKENER {
				if rarity == pb.MonsterRarity_MAGIC {
					count = 2
				} else if rarity >= pb.MonsterRarity_RARE {
					count = 3
				}
			}
			if getMonster(s, id).Activity != 10+int64(count*30) || eventCount(c.events, "stats_changed") != count {
				t.Fatal("wrong multiplier")
			}
		}
	}
}

func TestPotionMutagenKeepsQuantityBonus(t *testing.T) {
	s, r := fixture(t)
	a := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	b := put(s, 5, pb.Family_INSECT, pb.MonsterRarity_MAGIC, 30, 40)
	c := potionTestPlay(t, s, r, "mutagen_powder", a, b)
	for i, id := range []string{a, b} {
		m := getMonster(s, id)
		if m.Rarity != pb.MonsterRarity_RARE || m.Activity != int64(25+20*i) || m.Quantity != int64(84+20*i) {
			t.Fatalf("wrong mutagen effect: %v", m)
		}
	}
	if eventCount(c.events, "mutated") != 2 {
		t.Fatal("missing mutation events")
	}
}

func TestPotionAdditionBonuses(t *testing.T) {
	for _, tc := range []struct {
		id       string
		family   pb.Family
		activity int64
		quantity int64
	}{{"insect_powder", pb.Family_INSECT, 0, 73}, {"bone_powder", pb.Family_BONE, 0, 73}, {"awaker_fluid", pb.Family_AWAKENER, 31, 0}, {"fiend_fluid", pb.Family_FIEND, 31, 0}} {
		s, r := fixture(t)
		c := potionTestPlay(t, s, r, tc.id)
		m := s.Slots[0].Monster
		a, q := base(m.Rarity)
		if m.Family != tc.family || m.Activity != a+tc.activity || m.Quantity != q+tc.quantity || eventCount(c.events, "added") != 1 {
			t.Fatalf("wrong addition for %s: %v", tc.id, m)
		}
	}
}

func TestPotionBonePowderDefinitionAndOverflow(t *testing.T) {
	s, r := fixture(t)
	card := r.Card("bone_powder")
	if card == nil || card.Name != "细肢药粉-骨卫兵" || card.Rarity != pb.PotionRarity_WHITE || !card.Enabled || card.MinTargets != 0 || card.MaxTargets != 0 || !validTargets(s, card, nil) {
		t.Fatalf("invalid bone powder definition: %v", card)
	}
	for i := 0; i < 6; i++ {
		put(s, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
	}
	if validTargets(s, card, []string{s.Slots[0].Monster.Id}) {
		t.Fatal("bone powder accepted a selected target")
	}
	s.Tools = []string{"nail"}
	rng, nextID := s.EffectRng, s.NextMonsterId
	c := potionTestPlay(t, s, r, "bone_powder")
	if eventCount(c.events, "overflow") != 1 || eventCount(c.events, "added") != 0 || len(monsterIDs(s)) != 6 || s.EffectRng != rng || s.NextMonsterId != nextID {
		t.Fatal("bone powder overflow changed spawn state")
	}
	if s.Slots[0].Monster.Activity != 26 || s.Slots[0].Monster.Quantity != 61 {
		t.Fatal("bone powder overflow applied incorrect stats")
	}
}

func TestPotionInsectBoostAndLure(t *testing.T) {
	s, r := fixture(t)
	a := put(s, 0, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 10, 20)
	b := put(s, 2, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 30, 40)
	x := put(s, 5, pb.Family_BONE, pb.MonsterRarity_NORMAL, 50, 60)
	potionTestPlay(t, s, r, "insect_boost")
	if getMonster(s, a).Activity != 32 || getMonster(s, b).Activity != 52 || getMonster(s, x).Activity != 50 {
		t.Fatal("wrong insect boost")
	}
	c := potionTestPlay(t, s, r, "lure")
	if eventCount(c.events, "added") != 3 || eventCount(c.events, "overflow") != 1 || getMonster(s, a).Activity != 42 || getMonster(s, a).Quantity != 30 || getMonster(s, x).Activity != 50 || getMonster(s, x).Quantity != 60 {
		t.Fatal("wrong lure overflow")
	}
}

func TestPotionRandomBranchesReplay(t *testing.T) {
	for _, card := range []string{"gray_marrow", "hollow_marrow", "fiend_anesthetic", "brood_hormone"} {
		t.Run(card, func(t *testing.T) {
			branches := map[int]bool{}
			for seed := uint64(0); seed < 128; seed++ {
				s, r := fixture(t)
				s.EffectRng = seed
				id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
				before := proto.Clone(s).(*pb.GameState)
				ids := []string{}
				if r.Card(card).MinTargets > 0 {
					ids = append(ids, id)
				}
				c := potionTestPlay(t, s, r, card, ids...)
				d := potionTestPlay(t, before, r, card, ids...)
				if !proto.Equal(c.state, d.state) || len(c.events) != len(d.events) {
					t.Fatal("random replay diverged")
				}
				kind := "mutated"
				if card == "brood_hormone" {
					kind = "added"
				}
				branches[eventCount(c.events, kind)] = true
				m := getMonster(s, id)
				activity, quantity := int64(10), int64(20)
				for _, event := range c.events {
					if event.Kind == "mutated" || event.Kind == "stats_changed" {
						activity += event.ActivityDelta
						quantity += event.QuantityDelta
					}
				}
				if m.Activity != activity || m.Quantity != quantity {
					t.Fatal("event deltas differ from random branch stats")
				}
				if card == "hollow_marrow" || card == "gray_marrow" {
					bonus := int64(0)
					for _, event := range c.events {
						if event.Kind == "stats_changed" {
							bonus += event.ActivityDelta
						}
					}
					want := int64(41)
					if card == "gray_marrow" && eventCount(c.events, "mutated") == 2 {
						want += 30
					}
					if bonus != want {
						t.Fatal("wrong conditional activity bonus")
					}
				}
				if card == "fiend_anesthetic" || card == "hollow_marrow" && eventCount(c.events, "mutated") == 1 || card == "gray_marrow" && eventCount(c.events, "mutated") == 2 {
					if m.Family != pb.Family_FIEND {
						t.Fatal("conditional mutation did not create fiend")
					}
				}
				if card == "brood_hormone" {
					for _, slot := range s.Slots {
						if added := slot.Monster; added != nil && added.Id != id {
							a, q := base(added.Rarity)
							if added.Family != pb.Family_INSECT || added.Activity != a || added.Quantity != q {
								t.Fatal("wrong brood addition")
							}
						}
					}
				}
			}
			if len(branches) != 2 {
				t.Fatalf("missing branch coverage: %v", branches)
			}
		})
	}
}

func TestPotionRemovalToolEventChain(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 10)
	put(s, 1, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 2)
	put(s, 5, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 3, 4)
	s.Tools = []string{"eye"}
	c := potionTestPlay(t, s, r, "bone_ointment")
	m := getMonster(s, id)
	if m.Activity != 196 || m.Quantity != 106 || eventCount(c.events, "removed") != 2 {
		t.Fatal("removal tool buffs did not combine with potion buffs")
	}
	triggers := 0
	for i, event := range c.events {
		if event.Source != "eye" {
			continue
		}
		triggers++
		if i == 0 || c.events[i-1].Kind != "removed" || event.ParentSequence != c.events[i-1].Sequence || event.ActivityDelta != 20 || event.QuantityDelta != 20 {
			t.Fatal("tool trigger lost removal parent")
		}
	}
	if triggers != 2 {
		t.Fatal("wrong removal trigger count")
	}
}

func TestPotionMutationToolEventChain(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	other := put(s, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 30, 40)
	s.Tools = []string{"growth", "liver"}
	c := potionTestPlay(t, s, r, "petrified_marrow", id)
	m := getMonster(s, id)
	a, q := base(m.Rarity)
	if m.Activity != 115+a || m.Quantity != 20+q || getMonster(s, other).Activity != 85 {
		t.Fatal("mutation trigger chain has wrong stats")
	}
	var mutation uint64
	triggerEvents := 0
	for _, event := range c.events {
		if event.Kind == "mutated" {
			mutation = event.Sequence
		}
		if event.Source == "growth" || event.Source == "liver" {
			triggerEvents++
			if mutation == 0 || event.ParentSequence != mutation {
				t.Fatal("mutation parent missing")
			}
		}
	}
	if triggerEvents != 4 {
		t.Fatal("wrong mutation trigger count")
	}
}

func TestPotionAlienHormoneTwoTargetsOverflow(t *testing.T) {
	s, r := fixture(t)
	ids := []string{}
	for i := 0; i < 6; i++ {
		ids = append(ids, put(s, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36))
	}
	c := potionTestPlay(t, s, r, "alien_hormone", ids[0], ids[5])
	if getMonster(s, ids[0]) != nil || getMonster(s, ids[5]) != nil || len(monsterIDs(s)) != 6 || eventCount(c.events, "removed") != 2 || eventCount(c.events, "added") != 2 || eventCount(c.events, "overflow") != 2 {
		t.Fatal("wrong two-target replacement overflow")
	}
}

func TestPotionBroodHormoneOverflowBranches(t *testing.T) {
	branches := map[int]bool{}
	for seed := uint64(0); seed < 32; seed++ {
		s, r := fixture(t)
		s.EffectRng = seed
		for i := 0; i < 6; i++ {
			put(s, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		}
		c := potionTestPlay(t, s, r, "brood_hormone")
		count := eventCount(c.events, "overflow")
		branches[count] = true
		if count != 1 && count != 3 || eventCount(c.events, "added") != 0 {
			t.Fatal("wrong overflow probability branch")
		}
	}
	if !branches[1] || !branches[3] {
		t.Fatal("missing overflow branch")
	}
}

func TestPotionHandlerOverflowRollsBack(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, math.MaxInt64, 1)
	s.Offer.CardIds = []string{"cleansing_ointment"}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "cleansing_ointment", TargetIds: []string{id}}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("handler overflow did not roll back")
	}
}

func TestPotionLeechesIncludeSelectionAndSampleOther(t *testing.T) {
	for _, card := range []string{"pure_leech", "mixed_leech"} {
		for partners := 0; partners <= 3; partners++ {
			seenPartners := map[string]bool{}
			for seed := uint64(0); seed < 64; seed++ {
				s, r := fixture(t)
				s.EffectRng = seed
				id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_MAGIC, 10, 20)
				for i := 1; i <= partners; i++ {
					family, rarity := pb.Family_BONE, pb.MonsterRarity_NORMAL
					if card == "mixed_leech" {
						family, rarity = pb.Family_FIEND, pb.MonsterRarity_MAGIC
					}
					put(s, i, family, rarity, 10, 20)
				}
				other := put(s, 5, pb.Family_INSECT, pb.MonsterRarity_BOSS, 10, 20)
				c := potionTestPlay(t, s, r, card, id)
				want := 1
				if partners > 0 {
					want = 2
				}
				if getMonster(s, id).Activity != 41 || getMonster(s, other).Activity != 10 || eventCount(c.events, "stats_changed") != want {
					t.Fatalf("wrong leech targets for %s with %d partners", card, partners)
				}
				seen := map[string]bool{}
				for _, event := range c.events {
					if event.Kind == "stats_changed" {
						target := event.TargetIds[0]
						if seen[target] || event.ActivityDelta != 31 || event.QuantityDelta != 0 {
							t.Fatal("leech sampled duplicate or wrong bonus")
						}
						seen[target] = true
						if target != id {
							seenPartners[target] = true
						}
					}
				}
			}
			if len(seenPartners) != partners {
				t.Fatal("leech partner sampling omitted eligible targets")
			}
		}
	}
}

func TestPotionWillPowderSamplesDifferentFamilies(t *testing.T) {
	for _, card := range []string{"will_powder", "strong_will_powder"} {
		for others := 0; others <= 3; others++ {
			for seed := uint64(0); seed < 32; seed++ {
				s, r := fixture(t)
				s.EffectRng = seed
				id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 20)
				ally := put(s, 5, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 2)
				for i := 1; i <= others; i++ {
					put(s, i, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 2)
				}
				s.Tools = []string{"eye"}
				before := proto.Clone(s).(*pb.GameState)
				c := potionTestPlay(t, s, r, card, id)
				d := potionTestPlay(t, before, r, card, id)
				count, quantity := 1, int64(127)
				if card == "strong_will_powder" {
					count, quantity = 2, 154
				}
				if count > others {
					count = others
				}
				m := getMonster(s, id)
				if m.Activity != 100+int64(count)*20 || m.Quantity != 20+quantity+int64(count)*20 || getMonster(s, ally) == nil || eventCount(c.events, "removed") != count || !proto.Equal(c.state, d.state) {
					t.Fatal("will powder removal or replay incorrect")
				}
				seen := map[string]bool{}
				for _, event := range c.events {
					if event.Kind == "removed" {
						target := event.TargetIds[0]
						if target == id || target == ally || seen[target] {
							t.Fatal("will powder removed ineligible or duplicate target")
						}
						seen[target] = true
					}
				}
			}
		}
	}
}

func TestPotionSpecifiedAwakenings(t *testing.T) {
	for original := pb.MonsterRarity_NORMAL; original <= pb.MonsterRarity_BOSS; original++ {
		for seed := uint64(0); seed < 32; seed++ {
			s, r := fixture(t)
			s.EffectRng = seed
			id := put(s, 0, pb.Family_BONE, original, 10, 20)
			c := potionTestPlay(t, s, r, "awaker_anesthetic", id)
			m := getMonster(s, id)
			if eventCount(c.events, "mutated") != 1 || eventCount(c.events, "awakened") != 1 || len(c.events) != 2 || c.events[0].Kind != "mutated" || c.events[1].Kind != "awakened" || m.Family != pb.Family_AWAKENER || m.Rarity != pb.MonsterRarity_MAGIC {
				t.Fatal("anesthetic transform stages incorrect")
			}
			if m.Activity != 15+c.events[0].ActivityDelta || m.Quantity != 44+c.events[0].QuantityDelta || c.events[1].ActivityDelta != 5 || c.events[1].QuantityDelta != 24 {
				t.Fatal("anesthetic destination bases incorrect")
			}
		}
		for _, family := range []pb.Family{pb.Family_AWAKENER, pb.Family_BONE} {
			s, r := fixture(t)
			id := put(s, 0, family, original, 10, 20)
			c := potionTestPlay(t, s, r, "brain_fog")
			m := getMonster(s, id)
			if family == pb.Family_AWAKENER {
				if m.Rarity != pb.MonsterRarity_RARE || m.Activity != 66 || m.Quantity != 32 || eventCount(c.events, "awakened") != 1 {
					t.Fatal("brain fog fixed rarity incorrect")
				}
			} else if m.Rarity != original || m.Activity != 51 || m.Quantity != 20 || eventCount(c.events, "awakened") != 0 {
				t.Fatal("brain fog changed non-awakener rarity")
			}
		}
	}
}

func TestPotionHolyWaterAllRarities(t *testing.T) {
	s, r := fixture(t)
	for i := 0; i < 4; i++ {
		put(s, i, pb.Family_AWAKENER, pb.MonsterRarity(i+1), 10, 20)
	}
	other := put(s, 5, pb.Family_BONE, pb.MonsterRarity_BOSS, 10, 20)
	beforeRNG, beforeNextID := s.EffectRng, s.NextMonsterId
	c := potionTestPlay(t, s, r, "holy_water")
	for i := 0; i < 4; i++ {
		m := s.Slots[i].Monster
		rarity := pb.MonsterRarity_BOSS
		if i == 0 {
			rarity = pb.MonsterRarity_RARE
		}
		a, q := base(rarity)
		if m.Rarity != rarity || m.Activity != 10+a || m.Quantity != 20+q {
			t.Fatalf("wrong holy water result: %v", m)
		}
	}
	if getMonster(s, other).Activity != 10 || getMonster(s, other).Quantity != 20 || s.EffectRng == beforeRNG || s.NextMonsterId != beforeNextID || eventCount(c.events, "awakened") != 4 || eventCount(c.events, "stats_changed") != 0 || eventCount(c.events, "added") != 0 || eventCount(c.events, "mutated") != 0 {
		t.Fatal("holy water applied unexpected side effects")
	}
}

func TestPotionFreshMarrowSampling(t *testing.T) {
	for _, groups := range [][2]int{{0, 4}, {1, 0}, {3, 0}, {4, 2}} {
		for seed := uint64(0); seed < 64; seed++ {
			s, r := fixture(t)
			s.EffectRng = seed
			for i := 0; i < groups[0]+groups[1]; i++ {
				family := pb.Family_BONE
				if i >= groups[0] {
					family = pb.Family_FIEND
				}
				put(s, i, family, pb.MonsterRarity_NORMAL, 10, 20)
			}
			before := proto.Clone(s).(*pb.GameState)
			c := potionTestPlay(t, s, r, "fresh_marrow_powder")
			d := potionTestPlay(t, before, r, "fresh_marrow_powder")
			want := groups[0]
			if want > 3 {
				want = 3
			}
			if eventCount(c.events, "mutated") != want || !proto.Equal(c.state, d.state) {
				t.Fatal("fresh marrow mutation count or replay incorrect")
			}
			mutations, buffs := map[string]bool{}, map[string]bool{}
			batches, expectedBuffs := 0, 0
			for _, event := range c.events {
				if event.Kind == "mutated" {
					if len(buffs) != expectedBuffs {
						t.Fatal("fresh marrow partial batch has wrong size")
					}
					id := event.TargetIds[0]
					if mutations[id] || getMonster(s, id).Family != pb.Family_FIEND {
						t.Fatal("fresh marrow repeated mutation target")
					}
					mutations[id] = true
					batches++
					expectedBuffs = groups[1] + batches
					if expectedBuffs > 3 {
						expectedBuffs = 3
					}
					buffs = map[string]bool{}
				} else if event.Kind == "stats_changed" {
					id := event.TargetIds[0]
					if buffs[id] || event.ActivityDelta != 0 || event.QuantityDelta != 42 || getMonster(s, id).Family != pb.Family_FIEND {
						t.Fatal("fresh marrow repeated buff target or wrong bonus")
					}
					buffs[id] = true
				}
			}
			if len(buffs) != expectedBuffs {
				t.Fatal("fresh marrow final batch has wrong size")
			}
		}
	}
}

func TestPotionProliferationRarityGroups(t *testing.T) {
	for groups := 0; groups <= 4; groups++ {
		for seed := uint64(0); seed < 32; seed++ {
			s, r := fixture(t)
			s.EffectRng = seed
			for i := 0; i < groups; i++ {
				put(s, i, pb.Family_BONE, pb.MonsterRarity(i+1), 10, 20)
			}
			if groups > 0 {
				put(s, 4, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 10, 20)
				put(s, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 10, 20)
			}
			c := potionTestPlay(t, s, r, "proliferation_powder")
			if eventCount(c.events, "stats_changed") != groups {
				t.Fatal("proliferation did not use available rarity groups")
			}
			seen := map[pb.MonsterRarity]bool{}
			for _, event := range c.events {
				m := getMonster(s, event.TargetIds[0])
				if seen[m.Rarity] || m.Activity != 10 || m.Quantity != 52 || event.QuantityDelta != 32 {
					t.Fatal("proliferation repeated rarity group or wrong stats")
				}
				seen[m.Rarity] = true
			}
		}
	}
}

func TestPotionEggshellCopiesClassification(t *testing.T) {
	for rarity := pb.MonsterRarity_NORMAL; rarity <= pb.MonsterRarity_BOSS; rarity++ {
		for _, family := range []pb.Family{pb.Family_BONE, pb.Family_INSECT} {
			s, r := fixture(t)
			id := put(s, 0, family, rarity, 777, 888)
			c := potionTestPlay(t, s, r, "eggshell_powder")
			if getMonster(s, id).Quantity != 913 || getMonster(s, id).Activity != 777 {
				t.Fatal("eggshell original bonus incorrect")
			}
			if family == pb.Family_BONE {
				if eventCount(c.events, "added") != 0 || len(monsterIDs(s)) != 1 {
					t.Fatal("eggshell copied non-insect")
				}
			} else {
				m := s.Slots[1].Monster
				a, q := base(rarity)
				if m == nil || m.Id == id || m.Family != family || m.Rarity != rarity || m.Activity != a || m.Quantity != q+25 || eventCount(c.events, "added") != 1 {
					t.Fatal("eggshell copied original stats instead of classification")
				}
			}
		}
	}
	s, r := fixture(t)
	for i := 0; i < 6; i++ {
		put(s, i, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 10, 20)
	}
	s.Tools = []string{"nail"}
	c := potionTestPlay(t, s, r, "eggshell_powder")
	if eventCount(c.events, "added") != 0 || eventCount(c.events, "overflow") != 1 || len(monsterIDs(s)) != 6 || s.Slots[0].Monster.Activity != 35 {
		t.Fatal("eggshell overflow or nail trigger incorrect")
	}
}

func TestPotionBoneGrowthConditionalRemoval(t *testing.T) {
	branches := map[bool]bool{}
	for seed := uint64(0); seed < 64; seed++ {
		s, r := fixture(t)
		s.EffectRng = seed
		bone := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
		other := put(s, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 10, 20)
		c := potionTestPlay(t, s, r, "bone_growth_powder")
		selectedBone := c.events[0].TargetIds[0] == bone
		branches[selectedBone] = true
		if selectedBone {
			if getMonster(s, bone).Quantity != 187 || getMonster(s, other) != nil || eventCount(c.events, "removed") != 1 {
				t.Fatal("bone growth bonus or removal incorrect")
			}
		} else if getMonster(s, bone).Quantity != 20 || getMonster(s, other).Quantity != 60 || eventCount(c.events, "removed") != 0 {
			t.Fatal("bone growth applied bone effect to non-bone")
		}
	}
	if len(branches) != 2 {
		t.Fatal("bone growth did not cover both family branches")
	}
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	c := potionTestPlay(t, s, r, "bone_growth_powder")
	if getMonster(s, id).Quantity != 187 || eventCount(c.events, "removed") != 0 {
		t.Fatal("bone growth failed with no removable monster")
	}
}

func TestPotionNewHandlersRollback(t *testing.T) {
	for _, card := range []string{"will_powder", "strong_will_powder", "mixed_leech", "pure_leech", "awaker_anesthetic", "brain_fog", "holy_water", "fresh_marrow_powder", "proliferation_powder", "eggshell_powder", "bone_growth_powder"} {
		t.Run(card, func(t *testing.T) {
			s, r := fixture(t)
			family := pb.Family_BONE
			if card == "holy_water" {
				family = pb.Family_AWAKENER
			}
			activity, quantity := int64(math.MaxInt64), int64(1)
			switch card {
			case "will_powder", "strong_will_powder", "proliferation_powder", "eggshell_powder", "bone_growth_powder":
				activity, quantity = 1, math.MaxInt64
			}
			id := put(s, 0, family, pb.MonsterRarity_BOSS, activity, quantity)
			if err := ValidateState(s, r); err != nil {
				t.Fatal(err)
			}
			s.Offer.CardIds = []string{card}
			ids := []string{}
			if r.Card(card).MinTargets > 0 {
				ids = append(ids, id)
			}
			before := proto.Clone(s)
			next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: card, TargetIds: ids}, r)
			if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
				t.Fatal("potion overflow did not roll back state and RNG")
			}
		})
	}
}

func TestPotionLeechRandomPartialRollback(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_MAGIC, 10, 20)
	put(s, 5, pb.Family_BONE, pb.MonsterRarity_MAGIC, math.MaxInt64, 1)
	s.Offer.CardIds = []string{"mixed_leech"}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "mixed_leech", TargetIds: []string{id}}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("leech did not roll back first buff and random sampling")
	}
}

func TestPotionFreshMarrowMutationEventChain(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	other := put(s, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 30, 40)
	s.Tools = []string{"growth", "liver"}
	c := potionTestPlay(t, s, r, "fresh_marrow_powder")
	m := getMonster(s, id)
	a, q := base(m.Rarity)
	if m.Activity != 65+a || m.Quantity != 62+q || getMonster(s, other).Activity != 85 || getMonster(s, other).Quantity != 82 {
		t.Fatal("fresh marrow did not combine mutation triggers with quantity buffs")
	}
	var mutation uint64
	triggers := 0
	for _, event := range c.events {
		if event.Kind == "mutated" {
			mutation = event.Sequence
		}
		if event.Source == "growth" || event.Source == "liver" {
			triggers++
			if mutation == 0 || event.ParentSequence != mutation {
				t.Fatal("fresh marrow mutation parent missing")
			}
		}
	}
	if triggers != 4 {
		t.Fatal("wrong fresh marrow mutation trigger count")
	}
}
