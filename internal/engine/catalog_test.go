package engine

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func TestCatalogMatchesManual(t *testing.T) {
	data, err := os.ReadFile("../../渴瘾玩法说明书.md")
	if err != nil {
		t.Fatal(err)
	}
	normalize := func(name string) string {
		return strings.NewReplacer("·", "-", " ", "", "（", "(", "）", ")").Replace(name)
	}
	expected := map[pb.CardKind]map[string]bool{pb.CardKind_TOOL: {}, pb.CardKind_POTION: {}}
	coreFamilies := map[string]pb.Family{}
	families := map[string]pb.Family{"骨卫兵": pb.Family_BONE, "异魔": pb.Family_FIEND, "觉醒者": pb.Family_AWAKENER, "蛊虫": pb.Family_INSECT}
	var kind pb.CardKind
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "### 手术用具":
			kind = pb.CardKind_TOOL
		case strings.HasPrefix(line, "### 药剂"):
			kind = pb.CardKind_POTION
		case strings.HasPrefix(line, "### "):
			kind = 0
		}
		if kind == 0 || !strings.HasPrefix(line, "- ") {
			continue
		}
		name := strings.SplitN(strings.TrimPrefix(line, "- "), "：", 2)[0]
		if kind == pb.CardKind_POTION {
			if strings.HasPrefix(name, "药剂箱(") {
				name = strings.SplitN(name, ")", 2)[0] + ")"
				expected[kind][normalize(name)] = true
				continue
			}
			if strings.HasPrefix(name, "稀有药剂箱") {
				expected[kind]["稀有药剂箱(小)"] = true
				expected[kind]["稀有药剂箱(大)"] = true
				continue
			}
			name = strings.SplitN(name, "(", 2)[0]
		}
		if kind == pb.CardKind_TOOL {
			name = normalize(name)
			if prefix, annotation, found := strings.Cut(name, "(核心"); found {
				family, ok := families[strings.TrimSuffix(annotation, ")")]
				if !ok {
					t.Fatalf("invalid core annotation: %s", name)
				}
				name = prefix
				coreFamilies[name] = family
			}
		}
		expected[kind][normalize(name)] = true
	}
	r := DemoRules()
	for kind, names := range expected {
		seen := map[string]bool{}
		for _, card := range r.Cards {
			if card.Kind != kind {
				continue
			}
			name := normalize(card.Name)
			if kind == pb.CardKind_TOOL && card.CoreFamily != coreFamilies[name] {
				t.Errorf("core family mismatch for %s: got %v, want %v", name, card.CoreFamily, coreFamilies[name])
			}
			if !names[name] || seen[name] {
				t.Errorf("unexpected or duplicate %s: %s", kind, card.Name)
			}
			seen[name] = true
		}
		for name := range names {
			if !seen[name] {
				t.Errorf("missing %s: %s", kind, name)
			}
		}
	}
}

func TestDisabledCardsCannotBeOfferedOrPlayed(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
	for _, card := range r.Cards {
		if card.Enabled {
			continue
		}
		t.Run(card.Id, func(t *testing.T) {
			state := proto.Clone(s).(*pb.GameState)
			state.Offer.Kind = card.Kind
			state.Offer.CardIds = []string{card.Id}
			before := proto.Clone(state)
			if targets := LegalTargets(state, card); len(targets) != 0 {
				t.Fatal("disabled card has legal targets")
			}
			if View(state, r).Cards[0].Playable {
				t.Fatal("disabled card is playable")
			}
			out, events, err := Apply(state, &pb.Command{Type: "choose", OfferId: state.Offer.Id, CardId: card.Id}, r)
			if err == nil || out != nil || events != nil || !proto.Equal(state, before) {
				t.Fatal("disabled card was not rejected atomically")
			}
		})
	}
	for seed := 0; seed < 128; seed++ {
		s.OfferRng = streamSeed(fmt.Sprintf("catalog-%d", seed), "offers")
		for _, kind := range []pb.CardKind{pb.CardKind_POTION, pb.CardKind_TOOL} {
			if err := makeOffer(s, r, kind, "base", 0); err != nil {
				t.Fatal(err)
			}
			for _, id := range s.Offer.CardIds {
				if !r.Card(id).Enabled {
					t.Fatal("disabled card was offered")
				}
			}
		}
	}
}

func TestPreviousContentVersionIsRejected(t *testing.T) {
	s, r := fixture(t)
	s.ContentVersion = "cards-v0"
	if err := ValidateState(s, r); err == nil || !strings.HasPrefix(err.Error(), "VERSION_UNAVAILABLE:") {
		t.Fatal("previous content version accepted")
	}
}

func TestExpandedCatalogFullRuns(t *testing.T) {
	r := DemoRules()
	for seed := 0; seed < 256; seed++ {
		s, err := New("run", "user", fmt.Sprintf("expanded-%d", seed), 2, r)
		if err != nil {
			t.Fatal(err)
		}
		for step := 0; s.Phase != pb.Phase_FINISHED; step++ {
			if step > 21 {
				t.Fatalf("seed %d did not finish", seed)
			}
			v := View(s, r)
			if len(v.Cards) == 0 {
				t.Fatalf("seed %d has no choices", seed)
			}
			choice := v.Cards[(seed+step)%len(v.Cards)]
			if !choice.Playable || len(choice.LegalTargets) == 0 {
				t.Fatalf("seed %d offered unusable card %s", seed, choice.Definition.Id)
			}
			targets := choice.LegalTargets[(seed+step)%len(choice.LegalTargets)].Ids
			cmd := &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: choice.Definition.Id, TargetIds: targets}
			before := proto.Clone(s).(*pb.GameState)
			next, events, err := Apply(s, cmd, r)
			if err != nil {
				t.Fatalf("seed %d step %d card %s: %v", seed, step, choice.Definition.Id, err)
			}
			if !proto.Equal(s, before) {
				t.Fatal("input checkpoint changed")
			}
			replayed, replayEvents, err := Apply(before, cmd, r)
			if err != nil || !proto.Equal(next, replayed) || len(events) != len(replayEvents) {
				t.Fatalf("seed %d step %d failed deterministic replay: %v", seed, step, err)
			}
			for i := range events {
				if !proto.Equal(events[i], replayEvents[i]) {
					t.Fatalf("seed %d step %d event %d drifted", seed, step, i)
				}
			}
			s = next
		}
		if s.BaseCursor != 11 || s.CompletedTurns < 11 || s.CompletedTurns > 13 {
			t.Fatalf("seed %d finished with invalid counters", seed)
		}
	}
}
