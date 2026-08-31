package engine

import (
	"fmt"
	"sort"
	"testing"

	pb "vorax/internal/protocol"

	"google.golang.org/protobuf/proto"
)

func corePool(r *Rules) []*pb.CardDefinition {
	pool := []*pb.CardDefinition{}
	for _, card := range r.Cards {
		if card.Enabled && card.Kind == pb.CardKind_TOOL && card.CoreFamily != 0 {
			pool = append(pool, card)
		}
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].Id < pool[j].Id })
	return pool
}

func TestCoreToolCatalog(t *testing.T) {
	r := DemoRules()
	counts := [5]int{}
	for _, card := range corePool(r) {
		counts[card.CoreFamily]++
	}
	if counts != [5]int{0, 3, 3, 2, 3} {
		t.Fatalf("unexpected core distribution: %v", counts)
	}
	for _, id := range []string{"cortex", "goat_suture", "saw", "eye", "statue", "brooding_butterfly"} {
		if r.Card(id).CoreFamily != 0 {
			t.Fatalf("non-core tool marked as core: %s", id)
		}
	}
}

func TestOpeningToolWeightsHaveExactGroupShares(t *testing.T) {
	all := corePool(DemoRules())
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		for mask := 1; mask < 1<<len(all); mask++ {
			pool := []*pb.CardDefinition{}
			for i, card := range all {
				if mask&(1<<i) != 0 {
					pool = append(pool, card)
				}
			}
			weights, total := openingToolWeights(pool, family)
			counts := make([]int, len(pool))
			for roll := 0; roll < total; roll++ {
				index := toolIndexAt(roll, weights)
				if index < 0 || index >= len(pool) {
					t.Fatalf("invalid weighted index: %d", index)
				}
				counts[index]++
			}
			matching, other, matchingWeight, otherWeight := 0, 0, 0, 0
			for i, card := range pool {
				if counts[i] != weights[i] || counts[i] == 0 {
					t.Fatalf("incorrect weight for %s", card.Id)
				}
				if card.CoreFamily == family {
					matching += counts[i]
					if matchingWeight != 0 && matchingWeight != counts[i] {
						t.Fatal("matching tools have unequal shares")
					}
					matchingWeight = counts[i]
				} else {
					other += counts[i]
					if otherWeight != 0 && otherWeight != counts[i] {
						t.Fatal("other tools have unequal shares")
					}
					otherWeight = counts[i]
				}
			}
			if matching > 0 && other > 0 {
				if matching*20 != total*11 || other*20 != total*9 {
					t.Fatalf("incorrect group shares: %d/%d of %d", matching, other, total)
				}
			} else if total != len(pool) {
				t.Fatal("remaining group is not uniform")
			}
		}
	}
}

func TestInitialToolFamilyCountsGroups(t *testing.T) {
	s, _ := fixture(t)
	put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 1)
	put(s, 3, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 1)
	put(s, 5, pb.Family_FIEND, pb.MonsterRarity_BOSS, 300, 100000)
	rng := s.InitRng
	if family := initialToolFamily(s); family != pb.Family_BONE || s.InitRng != rng {
		t.Fatalf("group count or RNG mismatch: %v", family)
	}
	s.Slots[0].Monster, s.Slots[3].Monster, s.Slots[5].Monster = nil, nil, nil
	if initialToolFamily(s) != 0 || s.InitRng != rng {
		t.Fatal("empty board selected a family")
	}
}

func TestInitialToolFamilyRandomizesOnlyTiedLeaders(t *testing.T) {
	for _, layout := range [][]pb.Family{{1, 2}, {1, 2, 3}, {1, 2, 3, 4}, {1, 1, 2, 2, 3}} {
		seen := map[pb.Family]bool{}
		counts := [5]int{}
		most := 0
		for _, family := range layout {
			counts[family]++
			if counts[family] > most {
				most = counts[family]
			}
		}
		leaders := []pb.Family{}
		for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
			if counts[family] == most {
				leaders = append(leaders, family)
			}
		}
		awakenerTied := false
		for _, family := range leaders {
			if family == pb.Family_AWAKENER {
				awakenerTied = true
			}
		}
		for seed := uint64(0); seed < 128; seed++ {
			s, _ := fixture(t)
			for i, family := range layout {
				put(s, i, family, pb.MonsterRarity_NORMAL, 1, 36)
			}
			s.InitRng = seed
			expectedRNG := seed
			expected := leaders[0]
			if awakenerTied {
				expected = pb.Family_AWAKENER
			} else {
				expected = leaders[randomN(&expectedRNG, len(leaders))]
			}
			family := initialToolFamily(s)
			if family != expected || s.InitRng != expectedRNG || counts[family] != most {
				t.Fatalf("invalid tie selection: %v", family)
			}
			seen[family] = true
		}
		if awakenerTied {
			if len(seen) != 1 || !seen[pb.Family_AWAKENER] {
				t.Fatalf("tie involving awakener must deterministically prefer it: %v", seen)
			}
		} else if len(seen) != len(leaders) {
			t.Fatalf("not all tied leaders were selected: %v", seen)
		}
	}
}

func assertOpeningTools(t *testing.T, s *pb.GameState, r *Rules) {
	t.Helper()
	if s.Offer.Kind != pb.CardKind_TOOL || s.Offer.RewardThreshold != 0 || len(s.Offer.CardIds) != 3 {
		t.Fatal("invalid opening offer")
	}
	seen := map[string]bool{}
	for _, id := range s.Offer.CardIds {
		card := r.Card(id)
		if card == nil || card.CoreFamily == 0 || seen[id] || contains(s.Tools, id) {
			t.Fatalf("invalid opening candidate: %s", id)
		}
		seen[id] = true
	}
}

func TestOpeningToolsAcrossPreparationAndRefreshRestore(t *testing.T) {
	for _, mode := range []string{"skip_unknown", "unknown_bones", "unknown_insects", "unknown_six"} {
		for seed := 0; seed < 64; seed++ {
			r := DemoRules()
			s, err := New("run", "user", fmt.Sprintf("core-%s-%d", mode, seed), 2, r)
			if err != nil {
				t.Fatal(err)
			}
			cmd := &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}
			if mode != "skip_unknown" {
				s.Offer.CardIds = []string{mode}
				cmd.Type, cmd.CardId = "choose", mode
			}
			s, _, err = Apply(s, cmd, r)
			if err != nil {
				t.Fatal(err)
			}
			assertOpeningTools(t, s, r)
			counts := [5]int{}
			for _, slot := range s.Slots {
				if slot.Monster != nil {
					counts[slot.Monster.Family]++
				}
			}
			family, initialRNG := s.OpeningToolFamily, s.InitRng
			if family < 1 || family > 4 || counts[family] == 0 {
				t.Fatal("missing initial family")
			}
			for _, count := range counts {
				if count > counts[family] {
					t.Fatal("selected a non-leading initial family")
				}
			}
			for refresh := 0; refresh < 2; refresh++ {
				data, err := proto.Marshal(s)
				if err != nil {
					t.Fatal(err)
				}
				restored := new(pb.GameState)
				if err := proto.Unmarshal(data, restored); err != nil {
					t.Fatal(err)
				}
				cmd = &pb.Command{Type: "refresh", OfferId: s.Offer.Id}
				next, _, err := Apply(s, cmd, r)
				if err != nil {
					t.Fatal(err)
				}
				replayed, _, err := Apply(restored, cmd, r)
				if err != nil || !proto.Equal(next, replayed) {
					t.Fatal("opening refresh changed after restore")
				}
				assertOpeningTools(t, next, r)
				if next.OpeningToolFamily != family || next.InitRng != initialRNG || next.BaseCursor != 0 || next.CompletedTurns != 0 || next.ToolRefreshes != int32(1-refresh) {
					t.Fatal("opening refresh changed family, clock or budget")
				}
				s = next
			}
		}
	}
}

func TestOpeningDrawUsesSavedFamilyAndWeights(t *testing.T) {
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		for seed := uint64(0); seed < 128; seed++ {
			s, r := fixture(t)
			put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
			s.OpeningToolFamily = family
			s.OfferRng = seed
			expectedRNG := seed
			pool := corePool(r)
			ids := []string{}
			for i := 0; i < 3; i++ {
				weights, total := openingToolWeights(pool, family)
				index := toolIndexAt(randomN(&expectedRNG, total), weights)
				ids = append(ids, pool[index].Id)
				pool = append(pool[:index], pool[index+1:]...)
			}
			if err := makeOffer(s, r, pb.CardKind_TOOL, "refresh", 0); err != nil {
				t.Fatal(err)
			}
			for i, id := range ids {
				if s.Offer.CardIds[i] != id {
					t.Fatalf("incorrect weighted offer: got %v, want %v", s.Offer.CardIds, ids)
				}
			}
			if s.OfferRng != expectedRNG || s.OpeningToolFamily != family {
				t.Fatal("opening draw changed family or used extra RNG")
			}
		}
	}
}

func TestLaterToolOffersAreUniformAndIncludeAllTools(t *testing.T) {
	r := DemoRules()
	seen := map[string]bool{}
	for _, threshold := range []int64{8000, 28000} {
		for seed := uint64(0); seed < 256; seed++ {
			for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
				s, _ := fixture(t)
				s.BaseCursor = int32(seed % 11)
				s.OpeningToolFamily = family
				s.Tools = []string{"claw", "eye"}
				s.OfferRng = seed
				expectedRNG := seed
				pool := []*pb.CardDefinition{}
				for _, card := range r.Cards {
					if card.Enabled && card.Kind == pb.CardKind_TOOL && !contains(s.Tools, card.Id) {
						pool = append(pool, card)
					}
				}
				sort.Slice(pool, func(i, j int) bool { return pool[i].Id < pool[j].Id })
				ids := []string{}
				for i := 0; i < 3; i++ {
					index := randomN(&expectedRNG, len(pool))
					ids = append(ids, pool[index].Id)
					pool = append(pool[:index], pool[index+1:]...)
				}
				if err := makeOffer(s, r, pb.CardKind_TOOL, "refresh", threshold); err != nil {
					t.Fatal(err)
				}
				for i, id := range ids {
					if s.Offer.CardIds[i] != id {
						t.Fatalf("later offer is biased: got %v, want %v", s.Offer.CardIds, ids)
					}
					seen[id] = true
				}
				if s.OfferRng != expectedRNG {
					t.Fatal("later offer consumed extra RNG")
				}
			}
		}
	}
	if len(seen) != 22 || seen["claw"] || seen["eye"] {
		t.Fatalf("incomplete or duplicate later pool: %v", seen)
	}
}

func TestOpeningRejectsNonCoreAndFailedRefreshRollsBack(t *testing.T) {
	s, r := fixture(t)
	s.Offer.Kind = pb.CardKind_TOOL
	s.Offer.CardIds = []string{"saw"}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "saw"}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("opening accepted non-core tool")
	}
	for _, card := range r.Cards {
		if card.CoreFamily != 0 && card.Id != "claw" && card.Id != "pituitary" {
			card.Enabled = false
		}
	}
	next, events, err = Apply(s, &pb.Command{Type: "refresh", OfferId: s.Offer.Id}, r)
	if err == nil || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("failed core refresh was not atomic")
	}
}

func TestOpeningFamilyAndCoreMetadataValidation(t *testing.T) {
	for _, family := range []pb.Family{-1, 0, 5} {
		s, r := fixture(t)
		s.OpeningToolFamily = family
		if ValidateState(s, r) == nil {
			t.Fatal("invalid opening family accepted")
		}
	}
	for _, mutate := range []func(*Rules){
		func(r *Rules) { r.Card("claw").CoreFamily = 5 },
		func(r *Rules) { r.Card("lure").CoreFamily = pb.Family_BONE },
		func(r *Rules) {
			for _, card := range r.Cards {
				if card.CoreFamily != 0 {
					card.Enabled = false
				}
			}
		},
	} {
		r := DemoRules()
		mutate(r)
		if r.Validate() == nil {
			t.Fatal("invalid core content accepted")
		}
	}
}
