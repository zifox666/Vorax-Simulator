package application

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func service(t *testing.T) *Service {
	t.Helper()
	signer, err := NewSigner("test-v1", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{Rules: engine.DemoRules(), Signer: signer}
}
func create(t *testing.T, svc *Service) *pb.RunResponse {
	t.Helper()
	out, err := svc.Create(&pb.CreateRunRequest{UserId: "test-user", RequestId: "request-123", PetRefreshes: 2, Seed: "seed-test"})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestCreateRetryCommandRetryAndRestart(t *testing.T) {
	svc := service(t)
	req := &pb.CreateRunRequest{UserId: "test-user", RequestId: "random-request-123", PetRefreshes: 2}
	a, err := svc.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.Create(req)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a, b) {
		t.Fatal("create retry changed seed or identity")
	}
	cmd := &pb.Command{Type: "skip_unknown", OfferId: a.View.State.Offer.Id}
	request := &pb.CommandRequest{StateToken: a.StateToken, RequestId: "command-123", ExpectedRevision: a.View.State.Revision, Command: cmd}
	a, err = svc.Command(a.View.State.RunId, request)
	if err != nil {
		t.Fatal(err)
	}
	b, err = svc.Command(b.View.State.RunId, request)
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a, b) {
		t.Fatal("command retry changed result")
	}
	if a.View.State.OpeningToolFamily < pb.Family_BONE || a.View.State.OpeningToolFamily > pb.Family_INSECT {
		t.Fatal("opening family was not signed into state")
	}
	for _, card := range a.View.Cards {
		if card.Definition.CoreFamily == pb.Family_FAMILY_UNSPECIFIED {
			t.Fatal("non-core card in opening response")
		}
	}
	restarted := service(t)
	restored, err := restarted.Restore(a.View.State.RunId, &pb.RestoreRequest{StateToken: a.StateToken})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(a.View, restored.View) || a.GameplayHash != restored.GameplayHash {
		t.Fatal("restart changed restored state")
	}
}

func TestSignedStateTamperWrongRunVersionAndRevision(t *testing.T) {
	svc := service(t)
	out := create(t, svc)
	parts := strings.Split(out.StateToken, ".")
	parts[1] = "A" + parts[1][1:]
	if _, err := svc.Restore(out.View.State.RunId, &pb.RestoreRequest{StateToken: strings.Join(parts, ".")}); err == nil {
		t.Fatal("tampered checkpoint accepted")
	}
	if _, err := svc.Restore("other-run", &pb.RestoreRequest{StateToken: out.StateToken}); err == nil {
		t.Fatal("wrong run accepted")
	}
	_, err := svc.Command(out.View.State.RunId, &pb.CommandRequest{StateToken: out.StateToken, RequestId: "command-123", ExpectedRevision: 999, Command: &pb.Command{Type: "skip_unknown", OfferId: out.View.State.Offer.Id}})
	if err == nil || ErrorCode(err) != "STALE_STATE" {
		t.Fatal("stale revision accepted")
	}
	out.View.State.RulesVersion = "old-v0"
	token, _ := svc.Signer.Seal(out.View.State)
	if _, err := svc.Restore(out.View.State.RunId, &pb.RestoreRequest{StateToken: token}); err == nil || ErrorCode(err) != "VERSION_UNAVAILABLE" {
		t.Fatal("old version silently substituted")
	}
}

func TestLocalKeyPersistsAndRetiredKeysRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	a, err := LoadLocalSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := LoadLocalSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Keys[a.Active], b.Keys[b.Active]) {
		t.Fatal("key regenerated on restart")
	}
	svc := service(t)
	out := create(t, svc)
	rotated, _ := NewSigner("new-key", bytes.Repeat([]byte{3}, 32))
	rotated.Keys[svc.Signer.Active] = svc.Signer.Keys[svc.Signer.Active]
	if _, err := rotated.Open(out.StateToken); err != nil {
		t.Fatal("retired key not accepted", err)
	}
}

func TestReplayVerifiesAllStateNotOnlyScore(t *testing.T) {
	svc := service(t)
	out := create(t, svc)
	initial := proto.Clone(out.View.State).(*pb.GameState)
	commands := []*pb.Command{}
	for out.View.State.Phase != pb.Phase_FINISHED {
		card := out.View.Cards[0]
		cmd := &pb.Command{Type: "choose", OfferId: out.View.State.Offer.Id, CardId: card.Definition.Id, TargetIds: card.LegalTargets[0].Ids}
		commands = append(commands, cmd)
		var err error
		out, err = svc.Command(out.View.State.RunId, &pb.CommandRequest{StateToken: out.StateToken, RequestId: "request-command", ExpectedRevision: out.View.State.Revision, Command: cmd})
		if err != nil {
			t.Fatal(err)
		}
	}
	req := &pb.ReplayRequest{Seed: initial.Seed, RulesVersion: initial.RulesVersion, ContentVersion: initial.ContentVersion, RngVersion: initial.RngVersion, PetRefreshes: initial.InitialPetRefreshes, Commands: commands, ExpectedGameplayHash: out.GameplayHash}
	result, err := svc.Replay(req)
	if err != nil || !result.Verified {
		t.Fatal("valid replay failed", err)
	}
	req.ExpectedGameplayHash = "wrong"
	if _, err := svc.Replay(req); err == nil || ErrorCode(err) != "REPLAY_MISMATCH" {
		t.Fatal("mismatched replay accepted")
	}
	req.RulesVersion = "unknown"
	if _, err := svc.Replay(req); err == nil || ErrorCode(err) != "VERSION_UNAVAILABLE" {
		t.Fatal("old replay accepted")
	}
}
