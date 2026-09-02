package transport

import (
	"encoding/json"
	"google.golang.org/protobuf/encoding/protojson"
	"net/http/httptest"
	"strings"
	"testing"

	"vorax/internal/ai"
	"vorax/internal/engine"
	pb "vorax/internal/protocol"
)

func visibleFixture() ai.VisibleInput {
	v := ai.VisibleInput{RulesVersion: engine.RulesVersion, ContentVersion: engine.ContentVersion,
		Phase: "CHOOSING", BaseCursor: 1, Tools: []string{"claw"}, Score: 36,
		Offer: ai.OfferView{Kind: 2}, CardIDs: []string{"awakening", "hollow_marrow", "fiend_fluid"},
		PotionRefreshes: 0, ToolRefreshes: 2,
		ToolClaims: []ai.ClaimView{{Threshold: 8000, Status: "LOCKED"}, {Threshold: 28000, Status: "LOCKED"}}}
	for i := int32(0); i < 6; i++ {
		v.Slots = append(v.Slots, ai.VisibleSlot{Index: i})
	}
	v.Slots[2] = ai.VisibleSlot{Index: 2, DefinitionID: "bone_soldier", Activity: 1, Quantity: 36}
	return v
}

func TestVisibleHTTPBoundary(t *testing.T) {
	r := testRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/ai/catalog", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var catalog struct {
		Cards    []ai.CatalogCard      `json:"cards"`
		Monsters []engine.MonsterEntry `json:"monsters"`
		Flow     ai.VisibleFlow        `json:"flow"`
	}
	if json.Unmarshal(w.Body.Bytes(), &catalog) != nil || len(catalog.Monsters) != 30 || catalog.Flow.PotionTurns != 8 {
		t.Fatal("incomplete catalog")
	}
	boxes := 0
	wakingSalts := false
	for _, card := range catalog.Cards {
		if card.BoxSize > 0 {
			boxes++
		}
		if card.ID == "waking_salts" && card.Name == "惊醒嗅盐" && card.Rarity == int32(pb.PotionRarity_RED) {
			wakingSalts = true
		}
	}
	if boxes != 4 || !wakingSalts {
		t.Fatal("incomplete potion catalog", boxes, wakingSalts)
	}
	request := func(v ai.VisibleInput) string {
		body, err := json.Marshal(map[string]any{"visible": v, "strategy": "random"})
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	w = post(r, "/api/v1/ai/visible", request(visibleFixture()), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Action      ai.Action      `json:"action"`
		Observation ai.Observation `json:"observation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Action.Type != "choose" || result.Observation.Slots[2].Family != 1 || result.Observation.Score != 36 {
		t.Fatal("incorrect canonical observation")
	}
	for _, c := range result.Observation.Cards {
		if !c.Playable || len(c.TargetSets) == 0 {
			t.Fatal("missing server-generated targets")
		}
		for _, targets := range c.TargetSets {
			for _, slot := range targets {
				if slot != 2 {
					t.Fatal("target index was shifted")
				}
			}
		}
	}
	for _, mutation := range []func(*ai.VisibleInput){
		func(v *ai.VisibleInput) { v.Score++ },
		func(v *ai.VisibleInput) { v.Slots[1].Index = 2 },
		func(v *ai.VisibleInput) { v.CardIDs[0] = "not_in_catalog" },
		func(v *ai.VisibleInput) { v.PotionRefreshes = -1 },
		func(v *ai.VisibleInput) { v.CompletedTurns++ },
		func(v *ai.VisibleInput) { v.Tools[0] = "missing_tool" },
		func(v *ai.VisibleInput) { v.ToolClaims[1].Status = "PENDING" },
	} {
		v := visibleFixture()
		mutation(&v)
		if w := post(r, "/api/v1/ai/visible", request(v), ""); w.Code != 400 {
			t.Fatal("invalid OCR accepted", w.Body.String())
		}
	}
	for _, extra := range []string{`"seed":"secret",`, `"stateToken":"secret",`, `"targetSets":[[4]],`} {
		body := strings.Replace(request(visibleFixture()), `"visible":{`, `"visible":{`+extra, 1)
		if w := post(r, "/api/v1/ai/visible", body, ""); w.Code != 400 {
			t.Fatal("hidden or computed field accepted")
		}
	}
}

func TestPublicLocalModelInputsMatchTrainingSpecification(t *testing.T) {
	r := testRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/ai/model/spec", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var spec pb.TrainingSpec
	if err := protojson.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.SpecHash == "" || spec.Tensor.ActionCount != int32(len(spec.Actions)) {
		t.Fatal("incomplete public model specification")
	}
	body, err := json.Marshal(map[string]any{"visible": visibleFixture()})
	if err != nil {
		t.Fatal(err)
	}
	w = post(r, "/api/v1/ai/model/input", string(body), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var transition pb.TrainingTransition
	if err := protojson.Unmarshal(w.Body.Bytes(), &transition); err != nil {
		t.Fatal(err)
	}
	if transition.TensorObservation == nil || len(transition.ActionMask) != len(spec.Actions) {
		t.Fatal("incomplete model input")
	}
	legal := 0
	for _, allowed := range transition.ActionMask {
		if allowed == 1 {
			legal++
		}
	}
	if legal == 0 || legal != len(transition.LegalActions) || transition.Info.SpecVersion != spec.SpecVersion {
		t.Fatal("model input and action mask do not match specification")
	}
}

func TestVisibleErrorsIdentifyFields(t *testing.T) {
	r := testRouter(t)
	for _, tc := range []struct {
		name   string
		change func(*ai.VisibleInput)
		want   []string
	}{
		{"incomplete", func(v *ai.VisibleInput) {
			v.Slots = v.Slots[:4]
			v.ToolClaims = nil
			v.PotionRefreshes = -1
			v.ToolRefreshes = 3
		}, []string{"slots", "收到 4 个", "toolClaims", "收到 0 条", "potionRefreshes", "toolRefreshes"}},
		{"slot index", func(v *ai.VisibleInput) { v.Slots[2].Index = 4 }, []string{"第 3 号", "slots[2].index", "收到 4"}},
		{"slot stats", func(v *ai.VisibleInput) { v.Slots[2].Activity = 0; v.Slots[2].Quantity = 0 }, []string{"第 3 号", "士兵", "activity", "quantity"}},
		{"unknown monster", func(v *ai.VisibleInput) { v.Slots[2].DefinitionID = "unknown_beast" }, []string{"第 3 号", "definitionId", "unknown_beast"}},
		{"missing monster", func(v *ai.VisibleInput) { v.Slots[2].DefinitionID = "" }, []string{"第 3 号", "缺少 definitionId", "quantity=36"}},
		{"unknown card", func(v *ai.VisibleInput) { v.CardIDs[1] = "unknown_potion" }, []string{"cardIds[1]", "unknown_potion", "未收录"}},
		{"duplicate card", func(v *ai.VisibleInput) { v.CardIDs[1] = v.CardIDs[0] }, []string{"cardIds[1]", "迷魂酊剂", "重复"}},
		{"missing cards", func(v *ai.VisibleInput) { v.CardIDs = nil }, []string{"cardIds", "收到 0 张"}},
		{"unknown tool", func(v *ai.VisibleInput) { v.Tools[0] = "unknown_tool" }, []string{"tools[0]", "unknown_tool"}},
		{"missing tool", func(v *ai.VisibleInput) { v.Tools = nil }, []string{"tools", "应有 1 件", "收到 0 件"}},
		{"score", func(v *ai.VisibleInput) { v.Score = 37 }, []string{"score", "收到 37", "计算为 36"}},
		{"claim", func(v *ai.VisibleInput) { v.ToolClaims[1].Threshold = 8000 }, []string{"toolClaims[1].threshold", "28000", "8000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := visibleFixture()
			tc.change(&v)
			body, err := json.Marshal(map[string]any{"visible": v, "strategy": "random"})
			if err != nil {
				t.Fatal(err)
			}
			w := post(r, "/api/v1/ai/visible", string(body), "")
			var response struct {
				Message string `json:"message"`
			}
			if w.Code != 400 || json.Unmarshal(w.Body.Bytes(), &response) != nil {
				t.Fatalf("invalid error response: %d %s", w.Code, w.Body.String())
			}
			for _, part := range tc.want {
				if !strings.Contains(response.Message, part) {
					t.Errorf("error %q does not identify %q", response.Message, part)
				}
			}
		})
	}
	for _, tc := range []struct{ body, want string }{
		{`{}`, "缺少 visible"},
		{`{"visible":null}`, "缺少 visible"},
		{`{"visible":{"slots":[{"activity":"bad"}]}}`, "activity"},
		{`{"visible":{"seed":"not-allowed"}}`, `unknown field`},
	} {
		w := post(r, "/api/v1/ai/visible", tc.body, "")
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.want) {
			t.Fatalf("missing request detail: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestVisibleExplicitUnknownToolsContinue(t *testing.T) {
	r := testRouter(t)
	v := visibleFixture()
	v.BaseCursor, v.CompletedTurns = 3, 3
	v.ToolClaims[0].Status = "CLAIMED"
	v.UnknownTools = 1
	body, err := json.Marshal(map[string]any{"visible": v, "strategy": "sampler", "rollouts": 1})
	if err != nil {
		t.Fatal(err)
	}
	w := post(r, "/api/v1/ai/visible", string(body), "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Action      ai.Action      `json:"action"`
		Observation ai.Observation `json:"observation"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Action.Type != "choose" || len(response.Observation.Tools) != 1 || response.Observation.Tools[0] != "claw" {
		t.Fatal("missing tool effect was invented or recommendation missing", w.Body.String())
	}
	if response.Observation.Rewards.ToolClaims[0].Status != "CLAIMED" || response.Observation.CompletedTurns != 3 {
		t.Fatal("acquisition progress lost", w.Body.String())
	}
	for _, count := range []int32{-1, 0, 2, 4} {
		v.UnknownTools = count
		if _, err := ai.FromVisible(&v); err == nil {
			t.Fatalf("incorrect unknown tool count accepted: %d", count)
		}
	}
}

func TestVisibleRepeatedToolsAcrossOffers(t *testing.T) {
	r := testRouter(t)
	for _, choosingTool := range []bool{true, false} {
		v := visibleFixture()
		v.BaseCursor, v.CompletedTurns = 2, 1
		v.ToolClaims[0].Status = "PENDING"
		if choosingTool {
			v.Offer = ai.OfferView{Kind: 3, RewardThreshold: 8000}
			v.CardIDs = []string{"claw", "saw", "eye"} // claw is already owned.
		} else {
			v.Tools = []string{"claw", "claw"}
			v.ToolClaims[0].Status = "CLAIMED"
			v.CompletedTurns = 2
		}
		body, err := json.Marshal(map[string]any{"visible": v, "strategy": "sampler", "rollouts": 1})
		if err != nil {
			t.Fatal(err)
		}
		w := post(r, "/api/v1/ai/visible", string(body), "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if !choosingTool {
			var response struct {
				Observation ai.Observation `json:"observation"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || len(response.Observation.Tools) != 2 {
				t.Fatal("duplicate acquisition lost", w.Body.String())
			}
		}
		v.CardIDs[1] = v.CardIDs[0]
		if _, err := ai.FromVisible(&v); err == nil {
			t.Fatal("duplicate card within one offer accepted")
		}
	}
}

func TestVisibleMatchesEngineFlow(t *testing.T) {
	rules := engine.DemoRules()
	s, err := engine.New("visible-flow", "ocr-test", "visible-seed", 0, rules)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err = engine.Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, rules)
	if err != nil {
		t.Fatal(err)
	}
	for step := 0; step < 20; step++ {
		o := ai.FromGameState(s)
		v := ai.VisibleInput{RulesVersion: engine.RulesVersion, ContentVersion: engine.ContentVersion,
			Phase: o.Phase, BaseCursor: o.BaseCursor, CompletedTurns: o.CompletedTurns,
			Score: o.Score, Tools: o.Tools, Offer: o.Offer, PotionRefreshes: o.PotionRefreshes,
			ToolRefreshes: o.ToolRefreshes, ToolClaims: o.Rewards.ToolClaims}
		for _, slot := range o.Slots {
			v.Slots = append(v.Slots, ai.VisibleSlot{Index: slot.Index, DefinitionID: slot.DefinitionID, Activity: slot.Activity, Quantity: slot.Quantity})
		}
		for _, card := range o.Cards {
			v.CardIDs = append(v.CardIDs, card.ID)
		}
		canonical, err := ai.FromVisible(&v)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		if canonical.Score != o.Score || canonical.CompletedTurns != o.CompletedTurns {
			t.Fatal("flow mismatch")
		}
		if o.Done() {
			return
		}
		var cmd *pb.Command
		for _, card := range engine.View(s, rules).Cards {
			if card.Playable && len(card.LegalTargets) > 0 {
				cmd = &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: card.Definition.Id, TargetIds: card.LegalTargets[0].Ids}
				break
			}
		}
		s, _, err = engine.Apply(s, cmd, rules)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("flow did not finish")
}
