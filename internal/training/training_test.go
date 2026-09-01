package training

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"vorax/internal/application"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func testTrainingService(t *testing.T) (*Service, *application.Signer) {
	t.Helper()
	signer, err := application.NewSigner("test-v1", []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(engine.DemoRules(), NewEpisodeCodec(signer))
	if err != nil {
		t.Fatal(err)
	}
	return service, signer
}

func TestTrainingDeterminismRewardAndInformationBoundary(t *testing.T) {
	service, _ := testTrainingService(t)
	first, err := service.Reset(&pb.TrainingResetRequest{Seed: "visible-boundary-secret", PetRefreshes: 2})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Reset(&pb.TrainingResetRequest{Seed: "visible-boundary-secret", PetRefreshes: 2})
	if err != nil {
		t.Fatal(err)
	}
	if first.EpisodeToken == second.EpisodeToken || !proto.Equal(first.Observation, second.Observation) {
		t.Fatal("encrypted tokens must use unique nonces while observations stay deterministic")
	}
	encoded, _ := json.Marshal(first)
	for _, forbidden := range []string{"visible-boundary-secret", "initRng", "offerRng", "effectRng", "runId", "stateToken"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("training output leaked %q", forbidden)
		}
	}
	if len(first.ActionMask) != int(service.Spec.Message.Tensor.ActionCount) || first.ActionMask[0] != 1 || len(first.TensorObservation.SlotMonsters) != 6 || len(first.TensorObservation.ToolCounts) != len(service.Spec.Message.ToolIds) {
		t.Fatal("incorrect fixed training shapes or mask")
	}
	a, err := service.Step(&pb.TrainingStepRequest{EpisodeToken: first.EpisodeToken, SelectedAction: &pb.TrainingStepRequest_ActionIndex{ActionIndex: 0}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.Step(&pb.TrainingStepRequest{EpisodeToken: second.EpisodeToken, SelectedAction: &pb.TrainingStepRequest_ActionIndex{ActionIndex: 0}})
	if err != nil {
		t.Fatal(err)
	}
	if a.Reward != b.Reward || !proto.Equal(a.Observation, b.Observation) {
		t.Fatal("same seed and action diverged")
	}

	current, total := a, a.Reward
	for steps := 0; !current.Terminated && steps < 64; steps++ {
		index := int32(-1)
		for i, allowed := range current.ActionMask {
			if allowed == 1 {
				index = int32(i)
				break
			}
		}
		if index < 0 {
			t.Fatal("non-terminal state has no legal action")
		}
		current, err = service.Step(&pb.TrainingStepRequest{EpisodeToken: current.EpisodeToken, SelectedAction: &pb.TrainingStepRequest_ActionIndex{ActionIndex: index}})
		if err != nil {
			t.Fatal(err)
		}
		total += current.Reward
	}
	if !current.Terminated || total != current.Info.Score {
		t.Fatalf("reward telescoping failed: total=%d score=%d", total, current.Info.Score)
	}
}

func TestEpisodeTokenTamperAndRotation(t *testing.T) {
	service, signer := testTrainingService(t)
	transition, err := service.Reset(&pb.TrainingResetRequest{Seed: "rotation"})
	if err != nil {
		t.Fatal(err)
	}
	tampered := transition.EpisodeToken[:len(transition.EpisodeToken)-1] + "A"
	if _, err := service.Codec.Open(tampered); err == nil {
		t.Fatal("tampered token accepted")
	}
	signer.Active = "next-v2"
	signer.Keys[signer.Active] = []byte("abcdef0123456789abcdef0123456789")
	if _, err := service.Codec.Open(transition.EpisodeToken); err != nil {
		t.Fatal("previous key token stopped working after rotation", err)
	}
}

func TestLocalKeyLifecycleAndMemoryBucket(t *testing.T) {
	store, err := OpenLocalKeyStore(t.TempDir() + "/keys.json")
	if err != nil {
		t.Fatal(err)
	}
	manager := NewKeyManager(store)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	manager.Now = func() time.Time { return base }
	created, err := manager.Create(context.Background(), &pb.CreateTrainingKeyRequest{Name: "test", Bucket: &pb.TokenBucketConfig{Capacity: 3, RefillTokensPerSecond: 1}})
	if err != nil || created.Secret == "" {
		t.Fatal("key creation failed", err)
	}
	if _, err := manager.Authenticate(context.Background(), created.Secret); err != nil {
		t.Fatal("created key did not authenticate", err)
	}
	listed, err := manager.List(context.Background())
	if err != nil || len(listed.Keys) != 1 || strings.Contains(listed.String(), created.Secret) {
		t.Fatal("key listing exposed or lost secret")
	}
	limiter := NewMemoryBucketLimiter()
	limiter.now = func() time.Time { return base }
	if result, _ := limiter.Allow(context.Background(), created.Key.Id, 2, 3, 1); !result.Allowed || result.Remaining != 1 {
		t.Fatal("initial bucket charge incorrect", result)
	}
	if result, _ := limiter.Allow(context.Background(), created.Key.Id, 2, 3, 1); result.Allowed || result.RetryAfter <= 0 {
		t.Fatal("bucket did not limit by environment cost", result)
	}
	if _, err := manager.Revoke(context.Background(), created.Key.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(context.Background(), created.Secret); err == nil {
		t.Fatal("revoked key authenticated")
	}
}
