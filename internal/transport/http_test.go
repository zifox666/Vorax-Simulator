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
	return testRouterWithPublicOrigin(t, "")
}

func testRouterWithPublicOrigin(t *testing.T, publicOrigin string) http.Handler {
	t.Helper()
	signer, err := application.NewSigner("v1", bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	var assets fs.FS = fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}}
	r, err := Router(&application.Service{Rules: engine.DemoRules(), Signer: signer}, nil, http.FS(assets), publicOrigin)
	if err != nil {
		t.Fatal(err)
	}
	return r
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
// TestAIDecideObservationModeUsesRefreshes 验证 observation 模式：外部请求传入的
// 剩余刷新次数（药剂/用具分开）被正确使用——用具候选 + toolRefreshes=0 时不可能返回刷新。
func TestAIDecideObservationModeUsesRefreshes(t *testing.T) {
	r := testRouter(t)
	obs := `{"phase":"CHOOSING","baseCursor":0,"score":516,"potionRefreshes":0,"toolRefreshes":2,` +
		`"offer":{"kind":3,"rewardThreshold":0},` +
		`"cards":[{"id":"claw","name":"栾缩指爪","kind":3,"rarity":0,"playable":true,"targetSets":[[]]}],` +
		`"slots":[{"index":0,"family":1,"rarity":1,"activity":1,"quantity":36},{"index":1,"family":0,"rarity":0,"activity":0,"quantity":0},{"index":2,"family":0,"rarity":0,"activity":0,"quantity":0},{"index":3,"family":0,"rarity":0,"activity":0,"quantity":0},{"index":4,"family":0,"rarity":0,"activity":0,"quantity":0},{"index":5,"family":0,"rarity":0,"activity":0,"quantity":0}],` +
		`"rewards":{}}`
	body := func(o string) string { return `{"observation":` + o + `,"strategy":"random"}` }

	// toolRefreshes=0：用具候选没有刷新动作，random 只能选卡。
	w := post(r, "/api/v1/ai/decide", body(strings.Replace(obs, `"toolRefreshes":2`, `"toolRefreshes":0`, 1)), "")
	if w.Code != 200 {
		t.Fatalf("tool=0: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Action struct {
			Type string `json:"type"`
		} `json:"action"`
		Observation struct {
			ToolRefreshes   int32 `json:"toolRefreshes"`
			PotionRefreshes int32 `json:"potionRefreshes"`
		} `json:"observation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Action.Type != "choose" {
		t.Errorf("toolRefreshes=0 时不应返回刷新，得到 %q", resp.Action.Type)
	}
	if resp.Observation.ToolRefreshes != 0 || resp.Observation.PotionRefreshes != 0 {
		t.Errorf("响应观察应回显传入的刷新次数: tool=%d potion=%d", resp.Observation.ToolRefreshes, resp.Observation.PotionRefreshes)
	}

	// toolRefreshes=2：用具候选可以刷新。
	w = post(r, "/api/v1/ai/decide", body(obs), "")
	if w.Code != 200 {
		t.Fatalf("tool=2: %d %s", w.Code, w.Body.String())
	}
	resp = struct {
		Action struct {
			Type string `json:"type"`
		} `json:"action"`
		Observation struct {
			ToolRefreshes   int32 `json:"toolRefreshes"`
			PotionRefreshes int32 `json:"potionRefreshes"`
		} `json:"observation"`
	}{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Action.Type != "choose" && resp.Action.Type != "refresh" {
		t.Errorf("toolRefreshes=2 时应能选卡或刷新，得到 %q", resp.Action.Type)
	}

	// 药剂候选 + potionRefreshes=0（即使 toolRefreshes=2）：不可刷新。
	obsPotion := strings.Replace(obs, `"offer":{"kind":3,"rewardThreshold":0}`, `"offer":{"kind":2,"rewardThreshold":0}`, 1)
	obsPotion = strings.Replace(obsPotion, `"potionRefreshes":0`, `"potionRefreshes":0`, 1)
	obsPotion = strings.Replace(obsPotion, `"toolRefreshes":2`, `"toolRefreshes":2`, 1)
	w = post(r, "/api/v1/ai/decide", body(obsPotion), "")
	if w.Code != 200 {
		t.Fatalf("potion=0: %d %s", w.Code, w.Body.String())
	}
	var resp2 struct {
		Action struct {
			Type string `json:"type"`
		} `json:"action"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp2); err != nil {
		t.Fatal(err)
	}
	if resp2.Action.Type != "choose" {
		t.Errorf("药剂候选且 potionRefreshes=0 时不应返回刷新，得到 %q", resp2.Action.Type)
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

func TestHTTPAcceptsConfiguredPublicOriginBehindProxy(t *testing.T) {
	r := testRouterWithPublicOrigin(t, "https://ky.dscan.icu/")
	w := post(r, "/api/v1/runs", `{"userId":"proxy-user","requestId":"proxy-request"}`, "https://ky.dscan.icu")
	if w.Code != http.StatusOK {
		t.Fatalf("configured public origin rejected: %d %s", w.Code, w.Body.String())
	}
	w = post(r, "/api/v1/runs", `{}`, "https://other.example")
	if w.Code != http.StatusForbidden {
		t.Fatalf("unexpected origin accepted: %d %s", w.Code, w.Body.String())
	}
}

func TestRouterRejectsInvalidPublicOrigin(t *testing.T) {
	_, err := Router(nil, nil, nil, "https://ky.dscan.icu/path")
	if err == nil {
		t.Fatal("invalid public origin was accepted")
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
