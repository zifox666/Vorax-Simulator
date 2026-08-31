package engine

import (
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func TestPotionBoxDefinitions(t *testing.T) {
	r := DemoRules()
	for _, tc := range []struct {
		id   string
		size int
		rare bool
	}{{"normal_box_3", 3, false}, {"normal_box_5", 5, false}, {"box_3", 3, true}, {"box_5", 5, true}} {
		card := r.Card(tc.id)
		size, rare := potionBox(card)
		if card == nil || !card.Enabled || size != tc.size || rare != tc.rare || card.Rarity != pb.PotionRarity_GOLD || card.MinTargets != 0 || card.MaxTargets != 0 {
			t.Fatalf("invalid box %s: %v", tc.id, card)
		}
		name := "药剂箱(小)"
		if tc.size == 5 {
			name = "药剂箱(大)"
		}
		if tc.rare {
			name = "稀有" + name
		}
		if card.Name != name {
			t.Fatalf("box name %q, want %q", card.Name, name)
		}
	}
}

func TestPotionRarityWeights(t *testing.T) {
	for _, weights := range [][4]int{{40, 35, 20, 5}, {30, 20, 40, 10}, {0, 0, 1, 0}, {2, 3, 5, 7}} {
		counts := [4]int{}
		total := 0
		for _, weight := range weights {
			total += weight
		}
		for roll := 0; roll < total; roll++ {
			rarity := potionRarityAt(roll, weights)
			if rarity < pb.PotionRarity_WHITE || rarity > pb.PotionRarity_RED {
				t.Fatalf("invalid rarity at %d", roll)
			}
			counts[rarity-1]++
		}
		if counts != weights {
			t.Fatalf("rarity distribution %v, want %v", counts, weights)
		}
	}
}

func TestPotionBoxesUseConfiguredWeights(t *testing.T) {
	for _, tc := range []struct {
		id      string
		weights [4]int
	}{
		{"normal_box_3", [4]int{40, 35, 20, 5}},
		{"normal_box_5", [4]int{40, 35, 20, 5}},
		{"box_3", [4]int{30, 20, 40, 10}},
		{"box_5", [4]int{30, 20, 40, 10}},
	} {
		t.Run(tc.id, func(t *testing.T) {
			seen := map[pb.PotionRarity]bool{}
			for seed := 0; seed < 128; seed++ {
				s, r := fixture(t)
				put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
				s.BaseCursor = 1
				s.OfferRng = streamSeed(fmt.Sprintf("box-weights-%d", seed), "offers")
				s.Offer.CardIds = []string{tc.id}
				expected := proto.Clone(s).(*pb.GameState)
				size, _ := potionBox(r.Card(tc.id))
				ids, err := drawPotionCards(expected, r, size, tc.weights, 0)
				if err != nil {
					t.Fatal(err)
				}
				next, _, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: tc.id}, r)
				if err != nil || next.OfferRng != expected.OfferRng || len(next.Offer.CardIds) != len(ids) {
					t.Fatalf("seed %d box draw mismatch: %v", seed, err)
				}
				for i, id := range ids {
					if next.Offer.CardIds[i] != id {
						t.Fatalf("seed %d used incorrect weights: %v, want %v", seed, next.Offer.CardIds, ids)
					}
					seen[r.Card(id).Rarity] = true
				}
			}
			if len(seen) != 4 {
				t.Fatalf("missing rarity coverage: %v", seen)
			}
		})
	}
}

func TestPotionOffersHaveAtMostOneBox(t *testing.T) {
	r := DemoRules()
	seen := map[string]bool{}
	for seed := 0; seed < 1024; seed++ {
		s, err := New("run", "user", fmt.Sprintf("one-box-%d", seed), 2, r)
		if err != nil {
			t.Fatal(err)
		}
		put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		for _, weights := range [][4]int{{40, 35, 20, 5}, {0, 0, 1, 0}} {
			ids, err := drawPotionCards(s, r, 3, weights, 1)
			if err != nil {
				t.Fatal(err)
			}
			boxes := 0
			for _, id := range ids {
				if size, _ := potionBox(r.Card(id)); size > 0 {
					boxes++
					seen[id] = true
				}
			}
			if boxes > 1 {
				t.Fatalf("seed %d offered multiple boxes: %v", seed, ids)
			}
		}
	}
	for _, card := range r.Cards {
		if size, _ := potionBox(card); size > 0 && card.Enabled && !seen[card.Id] {
			t.Fatalf("box never offered: %s", card.Id)
		}
	}
}

func TestOpeningPotionBoxesPreservesClockAndBoard(t *testing.T) {
	for _, boxID := range []string{"normal_box_3", "normal_box_5", "box_3", "box_5"} {
		t.Run(boxID, func(t *testing.T) {
			s, r := fixture(t)
			put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 200, 200)
			s.Tools = []string{"saw", "statue"}
			s.BaseCursor = 1
			s.CompletedTurns = 1
			s.PotionRefreshes = 0
			s.Offer.CardIds = []string{boxID}
			before := proto.Clone(s).(*pb.GameState)
			cmd := &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: boxID}
			next, events, err := Apply(s, cmd, r)
			if err != nil {
				t.Fatal(err)
			}
			size, _ := potionBox(r.Card(boxID))
			if len(next.Offer.CardIds) != size || next.Offer.Id == s.Offer.Id || next.Offer.Source != "box:"+boxID || next.Revision != s.Revision+1 || len(events) != 1 || events[0].Kind != "box_opened" || events[0].Source != boxID {
				t.Fatal("incorrect box offer or event")
			}
			for _, id := range next.Offer.CardIds {
				if size, _ := potionBox(r.Card(id)); size > 0 {
					t.Fatal("box contained another box")
				}
				if len(LegalTargets(next, r.Card(id))) == 0 {
					t.Fatal("box contained unplayable potion")
				}
			}
			unchanged := proto.Clone(next).(*pb.GameState)
			unchanged.Revision = before.Revision
			unchanged.OfferRng = before.OfferRng
			unchanged.NextOfferId = before.NextOfferId
			unchanged.Offer = proto.Clone(before.Offer).(*pb.Offer)
			if !proto.Equal(before, unchanged) || !proto.Equal(before, s) {
				t.Fatal("opening box changed gameplay or original checkpoint")
			}
			replayed, replayEvents, err := Apply(s, cmd, r)
			if err != nil || !proto.Equal(next, replayed) || len(replayEvents) != 1 || !proto.Equal(events[0], replayEvents[0]) {
				t.Fatal("opening box failed deterministic replay")
			}
			choice := View(next, r).Cards[0]
			finished, _, err := Apply(next, &pb.Command{Type: "choose", OfferId: next.Offer.Id, CardId: choice.Definition.Id, TargetIds: choice.LegalTargets[0].Ids}, r)
			if err != nil || finished.CompletedTurns != s.CompletedTurns+1 || finished.BaseCursor != s.BaseCursor+1 {
				t.Fatalf("box potion did not consume exactly one turn: %v", err)
			}
			if _, _, err := Apply(next, cmd, r); err == nil {
				t.Fatal("stale box command accepted")
			}
		})
	}
}

func TestPotionBoxPoolsExcludeBoxes(t *testing.T) {
	for seed := 0; seed < 128; seed++ {
		s, r := fixture(t)
		s.OfferRng = uint64(seed)
		for _, count := range []int{3, 5} {
			ids, err := drawPotionCards(s, r, count, [4]int{0, 0, 1, 0}, 0)
			if err != nil || len(ids) != count {
				t.Fatalf("box draw failed: %v", err)
			}
			for _, id := range ids {
				if size, _ := potionBox(r.Card(id)); size > 0 {
					t.Fatal("box pool contained box")
				}
			}
		}
	}
}

func TestPotionDrawsHaveNoDuplicates(t *testing.T) {
	for seed := 0; seed < 512; seed++ {
		s, r := fixture(t)
		put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		s.OfferRng = uint64(seed)
		for _, tc := range []struct {
			count     int
			weights   [4]int
			boxLimit  int
			withBones bool
		}{
			{3, [4]int{40, 35, 20, 5}, 1, false},
			{3, [4]int{30, 20, 40, 10}, 1, false},
			{3, [4]int{40, 35, 20, 5}, 0, false},
			{5, [4]int{30, 20, 40, 10}, 0, false},
			{5, [4]int{0, 0, 1, 0}, 0, false},
		} {
			ids, err := drawPotionCards(s, r, tc.count, tc.weights, tc.boxLimit)
			if err != nil {
				t.Fatalf("seed %d weights %v count %d: %v", seed, tc.weights, tc.count, err)
			}
			if len(ids) != tc.count {
				t.Fatalf("seed %d: got %d ids, want %d", seed, len(ids), tc.count)
			}
			seen := map[string]bool{}
			for _, id := range ids {
				if seen[id] {
					t.Fatalf("seed %d weights %v count %d: duplicate card %s in %v", seed, tc.weights, tc.count, id, ids)
				}
				seen[id] = true
			}
		}
	}
}

func TestPotionBoxFailedDrawRollsBack(t *testing.T) {
	s, r := fixture(t)
	s.Offer.CardIds = []string{"normal_box_3"}
	for _, card := range r.Cards {
		if card.Kind == pb.CardKind_POTION {
			if size, _ := potionBox(card); size == 0 {
				card.Enabled = false
			}
		}
	}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "normal_box_3"}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("failed box draw was not rolled back")
	}
}
