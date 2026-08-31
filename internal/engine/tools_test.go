package engine

import (
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func toolFixture(t *testing.T, tools ...string) *context {
	t.Helper()
	s, r := fixture(t)
	s.Tools = tools
	return &context{state: s, rules: r, limit: 512}
}

func toolEnd(t *testing.T, c *context) {
	t.Helper()
	c.state.CompletedTurns++
	c.emit("turn_end", "", nil, 0, 0)
	if c.err != nil {
		t.Fatal(c.err)
	}
}

func toolTotals(s *pb.GameState) (int64, int64) {
	var activity, quantity int64
	for _, slot := range s.Slots {
		if m := slot.Monster; m != nil {
			activity += m.Activity
			quantity += m.Quantity
		}
	}
	return activity, quantity
}

func TestToolCatalog(t *testing.T) {
	cards := toolCards()
	if len(cards) != 24 {
		t.Fatalf("got %d tools", len(cards))
	}
	seen := map[string]bool{}
	for _, card := range cards {
		if seen[card.Id] || card.Id == "" || card.Name == "" || card.Description == "" || card.Trigger == "" || card.Handler != card.Id || card.Kind != pb.CardKind_TOOL || card.MinTargets != 0 || card.MaxTargets != 0 || !card.Enabled {
			t.Fatalf("invalid tool: %v", card)
		}
		seen[card.Id] = true
	}
	for _, id := range []string{"eye", "saw", "scraper", "growth", "nail", "statue", "mouth", "cortex", "marrow"} {
		if !seen[id] {
			t.Fatalf("missing tool %s", id)
		}
	}
}

func TestToolStrictStatThresholds(t *testing.T) {
	cases := []struct {
		tool                string
		activity, quantity  int64
		addActivity, addQty int64
	}{
		{"saw", 150, 151, 0, 0}, {"saw", 151, 150, 0, 0}, {"saw", 151, 151, 15, 15},
		{"scraper", 1, 275, 0, 0}, {"scraper", 1, 276, 20, 0},
		{"fang", 255, 1, 0, 0}, {"fang", 256, 1, 0, 30},
	}
	for _, tc := range cases {
		c := toolFixture(t, tc.tool)
		id := put(c.state, 3, pb.Family_BONE, pb.MonsterRarity_NORMAL, tc.activity, tc.quantity)
		toolEnd(t, c)
		m := getMonster(c.state, id)
		if m.Activity != tc.activity+tc.addActivity || m.Quantity != tc.quantity+tc.addQty {
			t.Fatalf("%s (%d,%d): %v", tc.tool, tc.activity, tc.quantity, m)
		}
	}
}

func TestToolClawReadsRemovedFamily(t *testing.T) {
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		c := toolFixture(t, "claw")
		removed := put(c.state, 0, family, pb.MonsterRarity_NORMAL, 1, 36)
		survivor := put(c.state, 4, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		c.remove(removed)
		want := int64(36)
		if family != pb.Family_BONE {
			want += 150
		}
		if getMonster(c.state, survivor).Quantity != want || c.removedMonster != nil || eventCount(c.events, "removed") != 1 {
			t.Fatalf("family %v: %v", family, c.state)
		}
	}
	c := toolFixture(t, "claw", "eye", "metatarsal")
	id := put(c.state, 2, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 36)
	rng := c.state.EffectRng
	c.remove(id)
	if c.err != nil || len(monsterIDs(c.state)) != 0 || c.state.EffectRng != rng {
		t.Fatal("empty removal pool changed state")
	}
}

func TestToolMetatarsalUsesSurvivingBones(t *testing.T) {
	for _, removeBone := range []bool{false, true} {
		c := toolFixture(t, "metatarsal")
		bone := put(c.state, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		put(c.state, 1, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		other := put(c.state, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 36)
		want := int64(72 + 180)
		if removeBone {
			c.remove(bone)
			want = 72
		} else {
			c.remove(other)
		}
		_, quantity := toolTotals(c.state)
		if quantity != want {
			t.Fatalf("removeBone %v: quantity %d", removeBone, quantity)
		}
	}
}

func TestToolCortexFusionSamplesDistinctMagicGroups(t *testing.T) {
	for count := 0; count <= 4; count++ {
		c := toolFixture(t, "cortex", "eye", "marrow")
		put(c.state, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 5, 10)
		for i := 0; i < count; i++ {
			put(c.state, i+1, pb.Family_FIEND, pb.MonsterRarity_MAGIC, int64(i+1)*10, int64(i+1)*20)
		}
		activity, quantity := toolTotals(c.state)
		toolEnd(t, c)
		wantFusions := 0
		if count >= 2 {
			wantFusions = 1
		}
		if len(monsterIDs(c.state)) != count+1-wantFusions || eventCount(c.events, "fused") != wantFusions || eventCount(c.events, "removed") != 0 || eventCount(c.events, "added") != 0 {
			t.Fatalf("count %d: %v", count, c.events)
		}
		afterActivity, afterQty := toolTotals(c.state)
		if afterActivity != activity || afterQty != quantity {
			t.Fatal("fusion did not conserve attributes")
		}
		if count >= 2 {
			ids := c.family(pb.Family_AWAKENER)
			if len(ids) != 1 || getMonster(c.state, ids[0]).Rarity != pb.MonsterRarity_RARE {
				t.Fatal("fusion produced wrong family or rarity")
			}
		}
	}
}

func TestToolSinewRemovesLowestNonBoneBeforeTransfer(t *testing.T) {
	c := toolFixture(t, "sinew", "eye", "claw")
	put(c.state, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 10)
	put(c.state, 1, pb.Family_BONE, pb.MonsterRarity_NORMAL, 2, 10)
	prey := put(c.state, 3, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 7, 90)
	survivor := put(c.state, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 7, 10)
	toolEnd(t, c)
	if getMonster(c.state, prey) != nil || getMonster(c.state, survivor).Activity != 27 || eventCount(c.events, "removed") != 1 {
		t.Fatal("wrong removal or chained eye target")
	}
	var boneActivity int64
	for _, id := range c.family(pb.Family_BONE) {
		boneActivity += getMonster(c.state, id).Activity
	}
	if boneActivity != 10 {
		t.Fatalf("transfer changed removed activity: %d", boneActivity)
	}
	if len(c.events) != 5 || c.events[2].Source != "eye" || c.events[3].Source != "claw" || c.events[4].Source != "sinew" || c.events[2].ParentSequence != c.events[1].Sequence {
		t.Fatalf("incorrect trigger ordering: %v", c.events)
	}
	for count := 0; count <= 2; count++ {
		c = toolFixture(t, "sinew")
		for i := 0; i < count; i++ {
			put(c.state, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 1)
		}
		if count < 2 {
			put(c.state, 5, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 1)
		}
		before := len(monsterIDs(c.state))
		toolEnd(t, c)
		if len(monsterIDs(c.state)) != before || eventCount(c.events, "removed") != 0 {
			t.Fatal("sinew ran without two bones and a non-bone")
		}
	}
}

func TestToolRarityAndFamilyRequirements(t *testing.T) {
	for missing := 0; missing <= 3; missing++ {
		c := toolFixture(t, "tanned_restraint")
		for i, rarity := range []pb.MonsterRarity{pb.MonsterRarity_NORMAL, pb.MonsterRarity_MAGIC, pb.MonsterRarity_BOSS} {
			if missing != i {
				put(c.state, i, pb.Family_BONE, rarity, 10, 10)
			}
		}
		toolEnd(t, c)
		for _, slot := range c.state.Slots {
			if slot.Monster != nil {
				want := int64(10)
				if missing == 3 {
					want = 30
				}
				if slot.Monster.Activity != want {
					t.Fatal("restraint rarity requirement not met")
				}
			}
		}
	}
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		for rarity := pb.MonsterRarity_NORMAL; rarity <= pb.MonsterRarity_BOSS; rarity++ {
			c := toolFixture(t, "pituitary")
			id := put(c.state, 2, family, rarity, 10, 10)
			toolEnd(t, c)
			want := int64(10)
			if family == pb.Family_AWAKENER && rarity >= pb.MonsterRarity_RARE {
				want = 90
			}
			if getMonster(c.state, id).Activity != want {
				t.Fatalf("pituitary family %v rarity %v", family, rarity)
			}
		}
	}
	for count := 0; count <= 2; count++ {
		for _, eligible := range []bool{false, true} {
			c := toolFixture(t, "frontal_lobe")
			for i := 0; i < count; i++ {
				put(c.state, i, pb.Family_AWAKENER, pb.MonsterRarity_NORMAL, 10, 10)
			}
			if eligible {
				put(c.state, 5, pb.Family_BONE, pb.MonsterRarity_RARE, 10, 10)
			}
			before, _ := toolTotals(c.state)
			toolEnd(t, c)
			after, _ := toolTotals(c.state)
			want := int64(0)
			if count == 2 && eligible {
				want = 80
			}
			if after-before != want {
				t.Fatal("frontal lobe eligibility mismatch")
			}
		}
	}
}

func TestToolMarrowProbabilityAndMutationChain(t *testing.T) {
	var success, failure bool
	rarities := map[pb.MonsterRarity]bool{}
	for seed := uint64(0); seed < 64; seed++ {
		c := toolFixture(t, "marrow", "growth", "liver")
		put(c.state, 0, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 36)
		c.state.EffectRng = seed
		rng := seed
		pickMonsterDefinition(pb.Family_BONE, pb.MonsterRarity_MAGIC, &rng)
		mutates := randomN(&rng, 100) < 75
		id := c.add(pb.Family_BONE, pb.MonsterRarity_MAGIC, 0, 0, &c.state.EffectRng)
		m := getMonster(c.state, id)
		if mutates {
			success = true
			rarity := monsterRarity(&rng)
			pickMonsterDefinition(pb.Family_FIEND, rarity, &rng)
			activity, quantity := base(rarity)
			rarities[m.Rarity] = true
			if m.Family != pb.Family_FIEND || m.Rarity != rarity || m.Activity != 5+activity+35+20+80 || m.Quantity != 24+quantity || c.state.Slots[0].Monster.Activity != 56 || eventCount(c.events, "mutated") != 1 {
				t.Fatalf("seed %d mutation chain: %v", seed, m)
			}
		} else {
			failure = true
			if m.Family != pb.Family_BONE || m.Activity != 5 || m.Quantity != 24 || eventCount(c.events, "mutated") != 0 {
				t.Fatalf("seed %d failed roll changed monster", seed)
			}
		}
		if c.err != nil || c.state.EffectRng != rng {
			t.Fatal("marrow consumed unexpected random numbers")
		}
	}
	if !success || !failure {
		t.Fatal("probability branches were not exercised")
	}
	if len(rarities) != 4 {
		t.Fatalf("missing mutation rarities: %v", rarities)
	}
}

func TestToolMutationRequirements(t *testing.T) {
	for count := 0; count <= 2; count++ {
		c := toolFixture(t, "growth", "liver")
		for i := 0; i < count; i++ {
			put(c.state, i, pb.Family_FIEND, pb.MonsterRarity_NORMAL, 1, 36)
		}
		id := put(c.state, 5, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		c.transform(id, pb.Family_INSECT, pb.MonsterRarity_NORMAL, false, 0)
		want := int64(2)
		if count == 2 {
			want += 20
		}
		if getMonster(c.state, id).Activity != want {
			t.Fatal("mutation requirements mismatch")
		}
		c.events = nil
		c.transform(id, 0, 0, true, 1)
		if eventCount(c.events, "stats_changed") != 0 {
			t.Fatal("awakening triggered mutation tools")
		}
	}
}

func TestToolPupaRepeatedDistinctSampling(t *testing.T) {
	for count := 0; count <= 6; count++ {
		c := toolFixture(t, "pupa")
		for i := 0; i < 6; i++ {
			family := pb.Family_BONE
			if i < count {
				family = pb.Family_INSECT
			}
			put(c.state, i, family, pb.MonsterRarity_NORMAL, 1, 1)
		}
		toolEnd(t, c)
		_, quantity := toolTotals(c.state)
		if quantity != int64(6+count*count*8) || eventCount(c.events, "stats_changed") != count*count {
			t.Fatalf("pupa count %d: %d", count, quantity)
		}
		for repeat := 0; repeat < count; repeat++ {
			seen := map[string]bool{}
			for _, event := range c.events[1+repeat*count : 1+(repeat+1)*count] {
				id := event.TargetIds[0]
				if seen[id] {
					t.Fatal("pupa selected duplicate within repetition")
				}
				seen[id] = true
			}
		}
	}
}

func TestToolStatueFreezesRarityGroupsAndPeriod(t *testing.T) {
	c := toolFixture(t, "statue")
	ids := []string{}
	for i := 0; i < 4; i++ {
		ids = append(ids, put(c.state, i, pb.Family_BONE, pb.MonsterRarity(i+1), 10, 10))
	}
	toolEnd(t, c)
	if eventCount(c.events, "awakened") != 0 {
		t.Fatal("statue triggered on odd turn")
	}
	toolEnd(t, c)
	if eventCount(c.events, "awakened") != 3 {
		t.Fatal("statue did not awaken each eligible rarity exactly once")
	}
	for i, id := range ids {
		m := getMonster(c.state, id)
		want := pb.MonsterRarity(i + 2)
		if want > pb.MonsterRarity_BOSS {
			want = pb.MonsterRarity_BOSS
		}
		if m.Rarity != want || m.Family != pb.Family_BONE || (i == 3 && m.Activity != 10) {
			t.Fatal("statue crossed frozen rarity groups or changed boss")
		}
	}
}

func TestToolEggThresholdsAndOverflow(t *testing.T) {
	for insects := 0; insects <= 3; insects++ {
		c := toolFixture(t, "cluster_eggs")
		for i := 0; i < insects; i++ {
			put(c.state, i, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
		}
		toolEnd(t, c)
		want := insects
		if insects >= 3 {
			want++
			m := c.state.Slots[insects].Monster
			_, baseQty := base(m.Rarity)
			if m.Family != pb.Family_INSECT || m.Quantity != baseQty+100 {
				t.Fatal("cluster eggs spawned incorrect monster")
			}
		}
		if len(monsterIDs(c.state)) != want {
			t.Fatal("cluster eggs requirement mismatch")
		}
	}
	for initial := 0; initial <= 2; initial++ {
		c := toolFixture(t, "hatching_egg")
		for i := 0; i < initial; i++ {
			put(c.state, i, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
		}
		c.add(pb.Family_INSECT, pb.MonsterRarity_NORMAL, 0, 0, &c.state.EffectRng)
		for _, slot := range c.state.Slots {
			if slot.Monster != nil {
				want := int64(36)
				if initial >= 1 {
					want += 45
				}
				if slot.Monster.Quantity != want {
					t.Fatal("hatching egg failed to include added insect")
				}
			}
		}
	}
	c := toolFixture(t, "cluster_eggs", "nail", "hatching_egg", "marrow")
	for i := 0; i < 6; i++ {
		put(c.state, i, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
	}
	rng := c.state.EffectRng
	toolEnd(t, c)
	activity, quantity := toolTotals(c.state)
	if activity != 31 || quantity != 241 || eventCount(c.events, "overflow") != 1 || eventCount(c.events, "added") != 0 || c.state.EffectRng != rng || c.state.Slots[0].Monster.Activity != 26 {
		t.Fatal("overflow triggered added effects or selected wrong minimum")
	}
}

func TestToolMouthCountsAndLowestContribution(t *testing.T) {
	for count := 0; count <= 5; count++ {
		c := toolFixture(t, "mouth")
		for i := 0; i < count; i++ {
			put(c.state, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
		}
		toolEnd(t, c)
		want := count
		if count == 2 {
			want++
		}
		if len(monsterIDs(c.state)) != want {
			t.Fatal("mouth group-count branch mismatch")
		}
	}
	c := toolFixture(t, "mouth", "eye", "claw")
	for i := 0; i < 6; i++ {
		put(c.state, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 100)
	}
	eater := put(c.state, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 1)
	prey := put(c.state, 4, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 20, 6)
	toolEnd(t, c)
	m := getMonster(c.state, eater)
	if getMonster(c.state, prey) != nil || m.Activity != 120 || m.Quantity != 7 || len(monsterIDs(c.state)) != 5 || eventCount(c.events, "devoured") != 1 || eventCount(c.events, "removed") != 0 {
		t.Fatal("mouth did not exclude eater or use contribution")
	}
}

func TestToolOverflowRollsBackCommand(t *testing.T) {
	for _, tool := range []string{"saw", "scraper", "fang", "pituitary", "pupa"} {
		t.Run(tool, func(t *testing.T) {
			s, r := fixture(t)
			s.Tools = []string{tool}
			s.Offer = &pb.Offer{Id: "fixture", Kind: pb.CardKind_SCHEME, CardIds: []string{"scheme_0"}}
			s.BaseCursor = 9
			activity, quantity := int64(math.MaxInt64), int64(1)
			family, rarity := pb.Family_AWAKENER, pb.MonsterRarity_RARE
			switch tool {
			case "saw":
				activity, quantity = math.MaxInt64/151, 151
			case "scraper":
				activity, quantity = math.MaxInt64/276, 276
			case "fang":
				activity, quantity = 256, math.MaxInt64/256
			case "pupa":
				activity, quantity, family = 1, math.MaxInt64, pb.Family_INSECT
			}
			put(s, 0, family, rarity, activity, quantity)
			before := proto.Clone(s)
			next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: "fixture", CardId: "scheme_0"}, r)
			if err == nil || !strings.Contains(err.Error(), "NUMERIC_OVERFLOW") || next != nil || events != nil || !proto.Equal(s, before) {
				t.Fatalf("tool overflow did not roll back: %v", err)
			}
		})
	}
}

func TestToolHatchingBuffsEveryFamily(t *testing.T) {
	c := toolFixture(t, "hatching_egg")
	put(c.state, 0, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
	put(c.state, 2, pb.Family_INSECT, pb.MonsterRarity_NORMAL, 1, 36)
	put(c.state, 4, pb.Family_AWAKENER, pb.MonsterRarity_NORMAL, 1, 36)
	id := c.add(pb.Family_BONE, pb.MonsterRarity_NORMAL, 0, 0, &c.state.EffectRng)
	if id == "" || len(monsterIDs(c.state)) != 4 {
		t.Fatal("monster was not added")
	}
	for _, slot := range c.state.Slots {
		if slot.Monster != nil && slot.Monster.Quantity != 81 {
			t.Fatal("hatching egg omitted a monster family")
		}
	}
}

func TestToolUnknownHandlerFails(t *testing.T) {
	c := toolFixture(t)
	c.tool("missing", &pb.GameEvent{Kind: "turn_end"})
	if c.err == nil || !strings.Contains(c.err.Error(), "INVALID_CARD") || len(c.events) != 0 {
		t.Fatal("unknown handler was accepted")
	}
}

func TestToolGoatSutureFusesFrozenRarityGroups(t *testing.T) {
	for rarity := pb.MonsterRarity_NORMAL; rarity <= pb.MonsterRarity_BOSS; rarity++ {
		for count := 0; count <= 6; count++ {
			c := toolFixture(t, "goat_suture", "claw", "eye", "marrow", "hatching_egg")
			for i := 0; i < count; i++ {
				put(c.state, i, pb.Family_INSECT, rarity, int64(i+1)*10, int64(i+1)*20)
			}
			activity, quantity := toolTotals(c.state)
			toolEnd(t, c)
			fusions := count / 3
			if rarity == pb.MonsterRarity_BOSS {
				fusions = 0
			}
			if eventCount(c.events, "fused") != fusions || len(monsterIDs(c.state)) != count-2*fusions || eventCount(c.events, "removed") != 0 || eventCount(c.events, "added") != 0 || eventCount(c.events, "mutated") != 0 {
				t.Fatalf("rarity %v count %d: %v", rarity, count, c.events)
			}
			afterActivity, afterQuantity := toolTotals(c.state)
			if afterActivity != activity || afterQuantity != quantity {
				t.Fatal("goat suture did not conserve attributes")
			}
			if fusions > 0 && len(c.toolRarity(rarity+1)) != fusions {
				t.Fatal("goat suture used incorrect destination rarity")
			}
		}
	}
	for magicCount := 2; magicCount <= 3; magicCount++ {
		c := toolFixture(t, "goat_suture")
		for i := 0; i < 3; i++ {
			put(c.state, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 10)
		}
		for i := 0; i < magicCount; i++ {
			put(c.state, i+3, pb.Family_BONE, pb.MonsterRarity_MAGIC, 20, 20)
		}
		activity, quantity := toolTotals(c.state)
		toolEnd(t, c)
		wantFusions := 1
		wantMagic := 3
		wantRare := 0
		if magicCount == 3 {
			wantFusions, wantMagic, wantRare = 2, 1, 1
		}
		if eventCount(c.events, "fused") != wantFusions || len(c.toolRarity(pb.MonsterRarity_MAGIC)) != wantMagic || len(c.toolRarity(pb.MonsterRarity_RARE)) != wantRare {
			t.Fatalf("magicCount %d did not respect original groups: %v", magicCount, c.events)
		}
		afterActivity, afterQuantity := toolTotals(c.state)
		if afterActivity != activity || afterQuantity != quantity {
			t.Fatal("multi-rarity fusion did not conserve attributes")
		}
	}
}

func TestToolGoatSutureAndStatueOrder(t *testing.T) {
	for _, order := range [][]string{{"statue", "goat_suture"}, {"goat_suture", "statue"}} {
		c := toolFixture(t, order...)
		for i := 0; i < 3; i++ {
			put(c.state, i, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 10)
		}
		c.state.CompletedTurns = 1
		toolEnd(t, c)
		if order[0] == "statue" {
			if len(monsterIDs(c.state)) != 3 || len(c.toolRarity(pb.MonsterRarity_NORMAL)) != 2 || len(c.toolRarity(pb.MonsterRarity_MAGIC)) != 1 || eventCount(c.events, "fused") != 0 {
				t.Fatal("goat suture ignored earlier statue awakening")
			}
		} else {
			ids := monsterIDs(c.state)
			if len(ids) != 1 || len(c.toolRarity(pb.MonsterRarity_RARE)) != 1 || eventCount(c.events, "fused") != 1 {
				t.Fatal("statue ignored earlier goat suture fusion")
			}
			m := getMonster(c.state, ids[0])
			if m.Activity != 45 || m.Quantity != 42 {
				t.Fatal("ordered fusion and awakening changed attribute totals")
			}
		}
	}
}

func TestToolThreeGroupMinimums(t *testing.T) {
	for _, handler := range []string{"rawhide_restraint", "nettle"} {
		for count := 0; count <= 6; count++ {
			c := toolFixture(t, handler)
			rarity := pb.MonsterRarity_MAGIC
			if handler == "nettle" {
				rarity = pb.MonsterRarity_NORMAL
			}
			for i := 0; i < 6; i++ {
				monsterRarity := pb.MonsterRarity_RARE
				if i < count {
					monsterRarity = rarity
				}
				put(c.state, i, pb.Family_BONE, monsterRarity, 10, 10)
			}
			c.state.Slots[5].Monster.Activity = 50
			toolEnd(t, c)
			for i, slot := range c.state.Slots {
				want := int64(10)
				if count >= 3 {
					if handler == "nettle" {
						want += 30
					} else if i == 5 {
						want += 100
					}
				}
				if slot.Monster.Quantity != want {
					t.Fatalf("%s count %d slot %d: %v", handler, count, i, slot.Monster)
				}
			}
		}
	}
}

func TestToolBroodingButterflyPeriodAndMinimum(t *testing.T) {
	for turn := int32(0); turn <= 6; turn++ {
		for count := 0; count <= 2; count++ {
			c := toolFixture(t, "brooding_butterfly")
			for i := 0; i < count; i++ {
				put(c.state, i, pb.Family_INSECT, pb.MonsterRarity_BOSS, 10, 10)
			}
			c.state.CompletedTurns = turn
			rng := c.state.EffectRng
			c.emit("turn_end", "", nil, 0, 0)
			want := 0
			if turn > 0 && turn%3 == 0 && count == 2 {
				want = 1
			}
			if c.err != nil || eventCount(c.events, "fused") != want || len(monsterIDs(c.state)) != count-want || (want == 0 && c.state.EffectRng != rng) {
				t.Fatalf("turn %d count %d: %v", turn, count, c.events)
			}
		}
	}
}

func TestToolBroodingButterflyRandomRarityAndConservation(t *testing.T) {
	rarities := map[pb.MonsterRarity]bool{}
	for seed := uint64(0); seed < 64; seed++ {
		c := toolFixture(t, "brooding_butterfly", "eye", "claw", "marrow", "hatching_egg")
		bone := put(c.state, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 5, 6)
		for i := 1; i <= 3; i++ {
			put(c.state, i, pb.Family_INSECT, pb.MonsterRarity_BOSS, int64(i)*20, int64(i)*30)
		}
		activity, quantity := toolTotals(c.state)
		c.state.EffectRng = seed
		c.state.CompletedTurns = 2
		toolEnd(t, c)
		if eventCount(c.events, "fused") != 1 || eventCount(c.events, "removed") != 0 || eventCount(c.events, "added") != 0 || eventCount(c.events, "mutated") != 0 || len(monsterIDs(c.state)) != 3 || getMonster(c.state, bone) == nil {
			t.Fatalf("seed %d: wrong fusion events or family selection", seed)
		}
		for _, event := range c.events {
			if event.Kind != "fused" {
				continue
			}
			if len(event.TargetIds) != 3 || event.TargetIds[0] == event.TargetIds[1] {
				t.Fatal("butterfly sampled duplicate insects")
			}
			m := getMonster(c.state, event.TargetIds[2])
			if m == nil || m.Family != pb.Family_INSECT {
				t.Fatal("butterfly did not produce insect")
			}
			rarities[m.Rarity] = true
		}
		afterActivity, afterQuantity := toolTotals(c.state)
		if afterActivity != activity || afterQuantity != quantity {
			t.Fatal("butterfly did not conserve attributes")
		}
	}
	if len(rarities) != 4 {
		t.Fatalf("missing fusion rarities: %v", rarities)
	}
}
