package telemetry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func TestRecordsPseudonymsAndVisibleObservationsOnly(t *testing.T) {
	r := &PostgresRecorder{key: bytes.Repeat([]byte{9}, 32)}
	state, err := engine.New("raw-run-id", "raw-browser-player-id", "training-seed", 2, engine.DemoRules())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	episode, err := r.episode(state, strings.Repeat("0", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if episode.ID == state.RunId || episode.PlayerPseudonym == state.UserId {
		t.Fatal("raw identifiers were not pseudonymized")
	}
	if episode.Seed != state.Seed {
		t.Fatal("seed required for replay was not retained")
	}
	serialized, err := json.Marshal(episode)
	if err != nil {
		t.Fatal(err)
	}
	text := string(serialized)
	for _, forbidden := range []string{"raw-run-id", "raw-browser-player-id", "initRng", "offerRng", "effectRng"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("private field %q leaked into episode", forbidden)
		}
	}

	command := &pb.Command{Type: "skip_unknown", OfferId: state.Offer.Id}
	next, events, err := engine.Apply(state, command, engine.DemoRules())
	if err != nil {
		t.Fatal(err)
	}
	transition, err := r.transition(state, next, command, events, strings.Repeat("a", 64), strings.Repeat("b", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	if transition.ActionType != "skip_unknown" || transition.SelectedTargetSlots != "[]" {
		t.Fatalf("decision was not normalized: %#v", transition)
	}
	serialized, err = json.Marshal(transition)
	if err != nil {
		t.Fatal(err)
	}
	text = string(serialized)
	for _, forbidden := range []string{"raw-run-id", "raw-browser-player-id", "training-seed", "initRng", "offerRng", "effectRng"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("private field %q leaked into transition", forbidden)
		}
	}

	retry, err := r.transition(state, next, command, events, strings.Repeat("a", 64), strings.Repeat("b", 64), now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != transition.ID {
		t.Fatal("retry produced a different idempotency key")
	}
}

func TestTargetMonsterIDsBecomeSlotIndexesAndBranchesDiffer(t *testing.T) {
	r := &PostgresRecorder{key: bytes.Repeat([]byte{5}, 32)}
	state := &pb.GameState{RunId: "run", UserId: "user", Revision: 7, Phase: pb.Phase_CHOOSING,
		Slots: []*pb.Slot{{Index: 0, Monster: &pb.Monster{Id: "monster-private-1"}}, {Index: 1}, {Index: 2, Monster: &pb.Monster{Id: "monster-private-2"}}}}
	slots, err := targetSlots(state, []string{"monster-private-2", "monster-private-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 || slots[0] != 2 || slots[1] != 0 {
		t.Fatalf("target order or slot normalization changed: %v", slots)
	}
	after := proto.Clone(state).(*pb.GameState)
	after.Revision++
	first, err := r.transition(state, after, &pb.Command{Type: "choose", CardId: "card-a", TargetIds: []string{"monster-private-2"}}, nil, strings.Repeat("c", 64), strings.Repeat("d", 64), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.transition(state, after, &pb.Command{Type: "choose", CardId: "card-b", TargetIds: []string{"monster-private-2"}}, nil, strings.Repeat("c", 64), strings.Repeat("e", 64), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID {
		t.Fatal("two branches from one checkpoint were collapsed")
	}
}
