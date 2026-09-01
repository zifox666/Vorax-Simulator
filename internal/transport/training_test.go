package transport

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"google.golang.org/protobuf/encoding/protojson"
	"vorax/internal/application"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
	"vorax/internal/training"
)

func trainingRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	signer, err := application.NewSigner("train-v1", bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	service, err := training.NewService(engine.DemoRules(), training.NewEpisodeCodec(signer))
	if err != nil {
		t.Fatal(err)
	}
	store, err := training.OpenLocalKeyStore(t.TempDir() + "/keys.json")
	if err != nil {
		t.Fatal(err)
	}
	keys := training.NewKeyManager(store)
	created, err := keys.Create(t.Context(), &pb.CreateTrainingKeyRequest{Name: "http", Bucket: &pb.TokenBucketConfig{Capacity: 8, RefillTokensPerSecond: 1}})
	if err != nil {
		t.Fatal(err)
	}
	var assets fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}, "admin.html": &fstest.MapFile{Data: []byte("admin")}}
	app := &application.Service{Rules: engine.DemoRules(), Signer: signer}
	router, err := RouterWithTraining(app, nil, http.FS(assets), "", &TrainingDependencies{Service: service, Keys: keys, Limiter: training.NewMemoryBucketLimiter(), AdminToken: "admin-secret"})
	if err != nil {
		t.Fatal(err)
	}
	return router, created.Secret
}

func trainingRequest(r http.Handler, method, path, body, secret string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if secret != "" {
		req.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestTrainingHTTPAuthResetStepBatchAndAdmin(t *testing.T) {
	router, secret := trainingRouter(t)
	if w := trainingRequest(router, "GET", "/api/v1/training/spec", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated spec accepted", w.Code)
	}
	if w := trainingRequest(router, "GET", "/admin", "", ""); w.Code != 200 || w.Body.String() != "admin" {
		t.Fatal("admin page missing", w.Code, w.Body.String())
	}
	w := trainingRequest(router, "POST", "/api/v1/training/reset", `{"seed":"http-seed","petRefreshes":2}`, secret)
	if w.Code != 200 || w.Header().Get("RateLimit-Remaining") != "7" {
		t.Fatal("reset failed", w.Code, w.Body.String())
	}
	var reset pb.TrainingTransition
	if err := protojson.Unmarshal(w.Body.Bytes(), &reset); err != nil || reset.EpisodeToken == "" || reset.ActionMask[0] != 1 {
		t.Fatal("invalid reset response", err, w.Body.String())
	}
	body, _ := protojson.Marshal(&pb.TrainingStepRequest{EpisodeToken: reset.EpisodeToken, SelectedAction: &pb.TrainingStepRequest_ActionIndex{ActionIndex: 0}})
	w = trainingRequest(router, "POST", "/api/v1/training/step", string(body), secret)
	if w.Code != 200 {
		t.Fatal("step failed", w.Code, w.Body.String())
	}
	batch := `{"items":[{"seed":"a"},{"seed":"b"},{"seed":"c"}]}`
	w = trainingRequest(router, "POST", "/api/v1/training/batch/reset", batch, secret)
	if w.Code != 200 || w.Header().Get("RateLimit-Remaining") != "3" {
		t.Fatal("batch did not charge per item", w.Code, w.Header(), w.Body.String())
	}
	w = trainingRequest(router, "POST", "/api/v1/admin/training-keys", `{"name":"new","bucket":{"capacity":"256","refillTokensPerSecond":64}}`, "admin-secret")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"secret":"vxtrain_`) {
		t.Fatal("admin key creation failed", w.Code, w.Body.String())
	}
}
