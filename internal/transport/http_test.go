package transport

import (
	"bytes"
	"encoding/json"
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
)

func testRouter(t *testing.T) http.Handler {
	t.Helper()
	signer, err := application.NewSigner("v1", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var assets fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}}
	return Router(&application.Service{Rules: engine.DemoRules(), Signer: signer}, nil, http.FS(assets))
}
func post(r http.Handler, path, body, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}
func TestHTTPProtoJSONAndRestore(t *testing.T) {
	r := testRouter(t)
	w := post(r, "/api/v1/runs", `{"userId":"test-user","requestId":"request-123","seed":"json-test","petRefreshes":1}`, "")
	if w.Code != 200 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	state := raw["view"].(map[string]any)["state"].(map[string]any)
	if _, ok := state["score"].(string); !ok {
		t.Fatal("int64 score must be a string")
	}
	if _, ok := state["revision"].(string); !ok {
		t.Fatal("uint64 revision must be a string")
	}
	out := new(pb.RunResponse)
	if err := protojson.Unmarshal(w.Body.Bytes(), out); err != nil {
		t.Fatal(err)
	}
	b, _ := protojson.Marshal(&pb.RestoreRequest{StateToken: out.StateToken})
	w = post(r, "/api/v1/runs/"+out.View.State.RunId+"/restore", string(b), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestHTTPRejectsUnknownFieldsBadOriginAndOversize(t *testing.T) {
	r := testRouter(t)
	for _, tc := range []struct {
		body, origin string
		status       int
	}{{`{"unknownField":1}`, "", 400}, {`{}`, "https://untrusted.example", 403}, {strings.Repeat("x", 300<<10), "", 413}} {
		w := post(r, "/api/v1/runs", tc.body, tc.origin)
		if w.Code != tc.status {
			t.Fatalf("want %d got %d", tc.status, w.Code)
		}
	}
}

func TestHTTPEventSnapshots(t *testing.T) {
	r := testRouter(t)
	w := post(r, "/api/v1/runs", `{"userId":"animation-user","requestId":"animation-create","seed":"animation-27"}`, "")
	created := new(pb.RunResponse)
	if w.Code != 200 || protojson.Unmarshal(w.Body.Bytes(), created) != nil {
		t.Fatal("create failed", w.Body.String())
	}
	body, err := protojson.Marshal(&pb.CommandRequest{
		StateToken: created.StateToken, ExpectedRevision: created.View.State.Revision, RequestId: "animation-command",
		Command: &pb.Command{Type: "skip_unknown", OfferId: created.View.State.Offer.Id},
	})
	if err != nil {
		t.Fatal(err)
	}
	w = post(r, "/api/v1/runs/"+created.View.State.RunId+"/commands", string(body), "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var raw struct {
		Events []struct {
			SlotsAfter []struct {
				Index   *int           `json:"index"`
				Monster map[string]any `json:"monster"`
			} `json:"slotsAfter"`
		} `json:"events"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw.Events) != 3 || len(raw.Events[0].SlotsAfter) != 6 {
		t.Fatal("missing animation snapshots")
	}
	first := raw.Events[0].SlotsAfter
	if first[0].Index == nil || *first[0].Index != 0 || first[1].Monster != nil {
		t.Fatal("snapshot loses zero index or empty slot")
	}
	if _, ok := first[0].Monster["activity"].(string); !ok {
		t.Fatal("snapshot activity must be an int64 string")
	}
}
