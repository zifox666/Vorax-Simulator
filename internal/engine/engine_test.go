package engine

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func fixture(t *testing.T) (*pb.GameState, *Rules) {
	t.Helper()
	r := DemoRules()
	s, err := New("run", "user", "fixture-seed", 2, r)
	if err != nil {
		t.Fatal(err)
	}
	s.Phase = pb.Phase_CHOOSING
	s.OpeningToolFamily = pb.Family_BONE
	s.Offer = &pb.Offer{Id: "fixture", Kind: pb.CardKind_POTION}
	return s, r
}

// TestMonsterRarityWeights 精确验证怪物稀有度权重 45/30/20/5：0..99999 个均匀随机数
// 每个余数恰好出现 1000 次，因此各稀有度计数必须精确等于权重 × 1000。
func TestMonsterRarityWeights(t *testing.T) {
	counts := [4]int{}
	for roll := 0; roll < 100000; roll++ {
		rarity := monsterRarityAt(roll % 100)
		if rarity < pb.MonsterRarity_NORMAL || rarity > pb.MonsterRarity_BOSS {
			t.Fatalf("invalid rarity at %d", roll)
		}
		counts[rarity-1]++
	}
	want := [4]int{45000, 30000, 20000, 5000}
	if counts != want {
		t.Fatalf("rarity distribution %v, want %v", counts, want)
	}
}
func put(s *pb.GameState, i int, f pb.Family, r pb.MonsterRarity, a, q int64) string {
	id := fmt.Sprintf("monster-%d", i+1)
	s.Slots[i].Monster = &pb.Monster{Id: id, Family: f, Rarity: r, Activity: a, Quantity: q}
	s.NextMonsterId = 10
	return id
}
func play(t *testing.T, s *pb.GameState, r *Rules, card string, targets ...string) (*pb.GameState, []*pb.GameEvent) {
	t.Helper()
	s.Offer.CardIds = []string{card}
	cmd := &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: card, TargetIds: targets}
	next, events, err := Apply(s, cmd, r)
	if err != nil {
		t.Fatal(err)
	}
	return next, events
}
func eventCount(events []*pb.GameEvent, kind string) int {
	n := 0
	for _, e := range events {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func TestRewardThresholdsAndRollback(t *testing.T) {
	cases := []struct {
		score                             int64
		white, purple, red, rainbow, drop int
	}{
		{0, 0, 0, 0, 0, 0}, {1999, 0, 0, 0, 0, 0}, {2000, 3, 0, 0, 0, 0}, {8000, 3, 0, 0, 0, 0},
		{18000, 6, 0, 0, 0, 0}, {28000, 6, 0, 0, 0, 0}, {38000, 5, 1, 0, 0, 0}, {57000, 4, 2, 0, 0, 0},
		{76000, 3, 3, 0, 0, 0}, {114000, 2, 4, 0, 0, 0}, {152000, 1, 5, 0, 0, 0}, {190000, 0, 6, 0, 0, 0},
		{266000, 0, 5, 1, 0, 0}, {342000, 0, 4, 2, 0, 0}, {418000, 0, 3, 3, 0, 0}, {512000, 0, 2, 4, 0, 0},
		{607000, 0, 1, 5, 0, 0}, {721000, 0, 0, 6, 0, 0}, {854000, 0, 0, 6, 0, 15}, {1120000, 0, 0, 5, 1, 15},
	}
	s, _ := fixture(t)
	// Walk both up and down: score-derived rewards are never a high-water mark.
	for pass := 0; pass < 2; pass++ {
		for j := range cases {
			i := j
			if pass == 1 {
				i = len(cases) - 1 - j
			}
			tc := cases[i]
			put(s, 0, 1, 1, tc.score, 1)
			if err := updateRewards(s, true); err != nil {
				t.Fatal(err)
			}
			counts := map[pb.JarColor]int{}
			for _, color := range s.Rewards.Jars {
				counts[color]++
			}
			if counts[pb.JarColor_JAR_WHITE] != tc.white || counts[pb.JarColor_PURPLE] != tc.purple || counts[pb.JarColor_JAR_RED] != tc.red || counts[pb.JarColor_RAINBOW] != tc.rainbow || s.Rewards.DropBonusPercent != int32(tc.drop) {
				t.Fatalf("score %d: %v", tc.score, s.Rewards)
			}
		}
	}
	if s.Rewards.ToolClaims[0].Status != pb.ClaimStatus_PENDING || s.Rewards.ToolClaims[1].Status != pb.ClaimStatus_PENDING {
		t.Fatal("unlocked tools were revoked")
	}
	for _, claim := range s.Rewards.ToolClaims {
		claim.Status = pb.ClaimStatus_CLAIMED
	}
	put(s, 0, 1, 1, 30000, 1)
	_ = updateRewards(s, true)
	for _, claim := range s.Rewards.ToolClaims {
		if claim.Status != pb.ClaimStatus_CLAIMED {
			t.Fatal("tool awarded twice")
		}
	}
}

func TestTransformationAddsDestinationBaseOnce(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, 1, 1, 1, 36)
	next, events := play(t, s, r, "awakening", id)
	m := next.Slots[0].Monster
	if m.Activity != 6 || m.Quantity != 60 || m.Rarity != pb.MonsterRarity_MAGIC || m.Family != pb.Family_BONE || eventCount(events, "awakened") != 1 {
		t.Fatalf("unexpected awakening: %v", m)
	}
	s, r = fixture(t)
	id = put(s, 0, 1, 1, 1, 36)
	next, _ = play(t, s, r, "mutation", id)
	m = next.Slots[0].Monster
	if m.Activity != 37 || m.Quantity != 60 || m.Rarity != pb.MonsterRarity_MAGIC {
		t.Fatalf("unexpected mutation: %v", m)
	}
	s, r = fixture(t)
	id = put(s, 0, 3, 4, 300, 1)
	next, events = play(t, s, r, "awakening", id)
	if next.Slots[0].Monster.Activity != 300 || eventCount(events, "awakened") != 0 {
		t.Fatal("boss gained free base stats")
	}
	s, r = fixture(t)
	id = put(s, 0, 3, 1, 1, 36)
	c := &context{state: s, rules: r, limit: 512}
	c.transform(id, 0, 0, true, 2)
	if c.err != nil {
		t.Fatal(c.err)
	}
	m = s.Slots[0].Monster
	if m.Activity != 16 || m.Quantity != 48 || m.Rarity != pb.MonsterRarity_RARE {
		t.Fatalf("two-stage awakening should add destination only: %v", m)
	}
}

func TestFusionAndDevourHaveIndependentEvents(t *testing.T) {
	for _, card := range []string{"fusion", "devour"} {
		t.Run(card, func(t *testing.T) {
			s, r := fixture(t)
			a := put(s, 0, 1, 1, 10, 20)
			b := put(s, 3, 2, 2, 30, 40)
			s.Tools = []string{"eye", "marrow"}
			c := &context{state: s, rules: r, limit: 512}
			if card == "fusion" {
				c.fuse([]string{a, b}, pb.Family_INSECT, 0)
			} else {
				c.devour(a, b)
			}
			if c.err != nil {
				t.Fatal(c.err)
			}
			next, events := s, c.events
			if eventCount(events, "removed") != 0 || eventCount(events, "added") != 0 || eventCount(events, "mutated") != 0 {
				t.Fatalf("composite leaked primitive events: %v", events)
			}
			m := next.Slots[0].Monster
			if m == nil || m.Activity != 40 || m.Quantity != 60 || next.Slots[3].Monster != nil {
				t.Fatalf("bad composite: %v", next.Slots)
			}
			if card == "fusion" && (m.Id == a || m.Family != pb.Family_INSECT || m.Rarity != pb.MonsterRarity_RARE) {
				t.Fatal("incorrect fusion identity")
			}
			if card == "devour" && m.Id != a {
				t.Fatal("devour replaced eater identity")
			}
		})
	}
}

func TestRemovalSkipsEmptySlotsAndTriggersOnce(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, 1, 1, 1, 36)
	id := put(s, 4, 2, 1, 10, 20)
	s.Tools = []string{"eye"}
	next, events := play(t, s, r, "digestive", id)
	if next.Slots[0].Monster != nil || next.Slots[4].Monster.Activity != 91 || next.Slots[4].Monster.Quantity != 40 || eventCount(events, "removed") != 1 {
		t.Fatal("left removal or eye trigger incorrect")
	}
	s, r = fixture(t)
	id = put(s, 0, 1, 1, 1, 36)
	next, events = play(t, s, r, "digestive", id)
	if next.Slots[0].Monster.Activity != 62 || eventCount(events, "removed") != 0 {
		t.Fatal("missing neighbour should be skipped")
	}
}

func TestOverflowHasNoAddedEvent(t *testing.T) {
	s, r := fixture(t)
	for i := 0; i < 6; i++ {
		put(s, i, 4, 1, 1, 36)
	}
	s.Tools = []string{"nail"}
	next, events := play(t, s, r, "lure")
	if eventCount(events, "overflow") != 4 || eventCount(events, "added") != 0 || len(monsterIDs(next)) != 6 {
		t.Fatal("incorrect overflow count")
	}
	var sum int64
	for _, slot := range next.Slots {
		sum += slot.Monster.Activity
	}
	if sum != 6+4*25+4*6*10 {
		t.Fatalf("missing overflow buffs: %d", sum)
	}
}

func TestTriggerOrderReadsUpdatedBoard(t *testing.T) {
	for _, order := range [][]string{{"scraper", "saw"}, {"saw", "scraper"}} {
		s, r := fixture(t)
		put(s, 0, 1, 1, 140, 300)
		s.Tools = order
		s.Offer.Kind = pb.CardKind_SCHEME
		s.BaseCursor = 8
		next, _ := play(t, s, r, "scheme_0")
		want := int64(160)
		if order[0] == "scraper" {
			want = 175
		}
		if next.Slots[0].Monster.Activity != want {
			t.Fatalf("wrong order %v: %v", order, next.Slots[0].Monster)
		}
	}
}

func TestNewToolRunsAndLateRewardsDelayFinish(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, 1, 1, 200, 200)
	s.Offer.Kind = pb.CardKind_TOOL
	s.Offer.RewardThreshold = 8000
	s.Rewards.ToolClaims[0].Status = pb.ClaimStatus_PENDING
	next, _ := play(t, s, r, "saw")
	if next.Slots[0].Monster.Activity != 215 || next.BaseCursor != 0 || next.CompletedTurns != 1 {
		t.Fatal("new tool did not participate")
	}
	s, r = fixture(t)
	put(s, 0, 1, 1, 1, 1400)
	s.Tools = []string{"scraper"}
	s.BaseCursor = 10
	s.CompletedTurns = 10
	s.Offer.Kind = pb.CardKind_SCHEME
	next, _ = play(t, s, r, "scheme_0")
	if next.Phase == pb.Phase_FINISHED || next.BaseCursor != 11 || next.Offer.RewardThreshold != 8000 {
		t.Fatal("last scheme discarded late reward")
	}
	next, _ = play(t, next, r, "nail")
	if next.Offer.RewardThreshold != 28000 {
		t.Fatal("second threshold not queued")
	}
	next, _ = play(t, next, r, "eye")
	if next.Phase != pb.Phase_FINISHED || next.CompletedTurns != 13 || next.BaseCursor != 11 {
		t.Fatal("late reward flow incorrect")
	}
}

func TestTransientThresholdDoesNotUnlock(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, 1, 1, 10, 200)
	id := put(s, 1, 1, 1, 1, 100)
	next, _ := play(t, s, r, "digestive", id) // 2,100 -> 8,200 -> 6,200
	if next.Score != 6200 || next.Rewards.ToolClaims[0].Status != pb.ClaimStatus_LOCKED {
		t.Fatal("transient threshold incorrectly unlocked")
	}
}

func TestRefreshDoesNotAdvanceClock(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, 1, 1, 1, 36)
	before := proto.Clone(s).(*pb.GameState)
	next, _, err := Apply(s, &pb.Command{Type: "refresh", OfferId: s.Offer.Id}, r)
	if err != nil {
		t.Fatal(err)
	}
	if next.CompletedTurns != 0 || next.BaseCursor != 0 || next.PotionRefreshes != 2 || !proto.Equal(before, s) {
		t.Fatal("refresh was not atomic or advanced clock")
	}
}

func TestPeriodicToolAndRaritySnapshot(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, 1, 1, 1, 36)
	s.Tools = []string{"statue"}
	s.Offer.Kind = pb.CardKind_SCHEME
	s.BaseCursor = 8
	next, events := play(t, s, r, "scheme_0")
	if eventCount(events, "awakened") != 0 {
		t.Fatal("periodic tool triggered early")
	}
	next, events = play(t, next, r, "scheme_0")
	m := getMonster(next, id)
	if m.Rarity != pb.MonsterRarity_MAGIC || eventCount(events, "awakened") != 1 {
		t.Fatal("periodic tool awakened same monster repeatedly")
	}
}

func TestFailedCommandRollsBackEverything(t *testing.T) {
	s, r := fixture(t)
	id := put(s, 0, 1, 1, math.MaxInt64, 1)
	s.Offer.CardIds = []string{"awakening"}
	before := proto.Clone(s)
	next, events, err := Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "awakening", TargetIds: []string{id}}, r)
	if err == nil || !strings.Contains(err.Error(), "NUMERIC_OVERFLOW") || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("overflow did not roll back")
	}
	s, r = fixture(t)
	id = put(s, 0, 1, 1, 1, 36)
	s.Tools = []string{"eye"}
	r.Card("eye").Trigger = "stats_changed"
	s.Offer.CardIds = []string{"digestive"}
	before = proto.Clone(s)
	next, events, err = Apply(s, &pb.Command{Type: "choose", OfferId: s.Offer.Id, CardId: "digestive", TargetIds: []string{id}}, r)
	if err == nil || !strings.Contains(err.Error(), "EFFECT_LIMIT") || next != nil || events != nil || !proto.Equal(s, before) {
		t.Fatal("recursive effect did not roll back")
	}
}

func TestInvalidTargetsAndRefreshLeaveRNGUnchanged(t *testing.T) {
	s, r := fixture(t)
	put(s, 0, 1, 1, 1, 36)
	s.Offer.CardIds = []string{"mutation"}
	s.PotionRefreshes = 0
	before := proto.Clone(s)
	for _, cmd := range []*pb.Command{{Type: "choose", OfferId: "fixture", CardId: "mutation", TargetIds: []string{"missing"}}, {Type: "choose", OfferId: "fixture", CardId: "mutation", TargetIds: []string{"monster-1", "monster-1"}}, {Type: "refresh", OfferId: "fixture"}, {Type: "choose", OfferId: "old", CardId: "mutation"}} {
		if _, _, err := Apply(s, cmd, r); err == nil {
			t.Fatal("invalid command accepted")
		}
		if !proto.Equal(s, before) {
			t.Fatal("invalid command changed original state")
		}
	}
}

func TestDeterministicFullRunsAcrossSaveRestore(t *testing.T) {
	for seed := 0; seed < 64; seed++ {
		r := DemoRules()
		a, err := New("a", "u", fmt.Sprintf("seed-%d", seed), 2, r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := New("b", "u", fmt.Sprintf("seed-%d", seed), 2, r)
		steps := 0
		potions, schemes, openingTools := 0, 0, 0
		ownedTools := map[string]bool{}
		for a.Phase != pb.Phase_FINISHED {
			if steps > 21 {
				t.Fatal("run did not terminate")
			}
			steps++
			v := View(a, r)
			var choice *pb.CardView
			for _, card := range v.Cards {
				if card.Playable {
					choice = card
					break
				}
			}
			if choice == nil {
				t.Fatalf("seed %d has no playable choices", seed)
			}
			switch choice.Definition.Kind {
			case pb.CardKind_POTION:
				if size, _ := potionBox(choice.Definition); size == 0 {
					potions++
				}
			case pb.CardKind_SCHEME:
				schemes++
			case pb.CardKind_TOOL:
				if ownedTools[choice.Definition.Id] {
					t.Fatal("duplicate tool offered")
				}
				ownedTools[choice.Definition.Id] = true
				if a.Offer.RewardThreshold == 0 {
					openingTools++
				}
			}
			cmd := &pb.Command{Type: "choose", OfferId: a.Offer.Id, CardId: choice.Definition.Id, TargetIds: choice.LegalTargets[0].Ids}
			a, _, err = Apply(a, cmd, r)
			if err != nil {
				t.Fatal(err)
			}
			blob, _ := proto.Marshal(b)
			b = new(pb.GameState)
			if err = proto.Unmarshal(blob, b); err != nil {
				t.Fatal(err)
			}
			b, _, err = Apply(b, cmd, r)
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(GameplayCopy(a), GameplayCopy(b)) {
				t.Fatalf("seed %d drift at step %d", seed, steps)
			}
		}
		if a.BaseCursor != 11 || a.CompletedTurns < 11 || a.CompletedTurns > 13 {
			t.Fatal("invalid final turn count")
		}
		if potions != 7 || schemes != 3 || openingTools != 1 {
			t.Fatalf("wrong flow: %d potions, %d schemes, %d opening tools", potions, schemes, openingTools)
		}
	}
}

func TestOpeningToolIsIndependentOfScoreRewards(t *testing.T) {
	r := DemoRules()
	s, err := New("opening-run", "user", "opening-seed", 2, r)
	if err != nil {
		t.Fatal(err)
	}
	s, _, err = Apply(s, &pb.Command{Type: "skip_unknown", OfferId: s.Offer.Id}, r)
	if err != nil {
		t.Fatal(err)
	}
	if s.Offer.Kind != pb.CardKind_TOOL || s.Offer.RewardThreshold != 0 || s.BaseCursor != 0 || s.CompletedTurns != 0 {
		t.Fatal("opening tool missing or consumed preparation turn")
	}
	s, _, err = Apply(s, &pb.Command{Type: "refresh", OfferId: s.Offer.Id}, r)
	if err != nil {
		t.Fatal(err)
	}
	if s.ToolRefreshes != 1 || s.CompletedTurns != 0 || s.Offer.RewardThreshold != 0 {
		t.Fatal("opening tool refresh changed clock or reward source")
	}
	s, _ = play(t, s, r, "claw")
	if s.BaseCursor != 1 || s.CompletedTurns != 1 || len(s.Tools) != 1 || s.Offer.Kind != pb.CardKind_POTION || View(s, r).StageLabel != "药剂选择 1 / 7" {
		t.Fatal("opening tool did not advance into seven-potion flow")
	}
	for _, claim := range s.Rewards.ToolClaims {
		if claim.Status != pb.ClaimStatus_LOCKED {
			t.Fatal("opening tool consumed a score reward")
		}
	}
	// Keep a low-scoring bone specimen so score rewards do not obscure the base schedule.
	for _, slot := range s.Slots {
		slot.Monster = nil
	}
	put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 1, 36)
	for i := 0; i < 7; i++ {
		if s.Offer.Kind != pb.CardKind_POTION {
			t.Fatal("potion count incorrect")
		}
		s, _ = play(t, s, r, "insect_boost")
	}
	for i := 0; i < 3; i++ {
		if s.Offer.Kind != pb.CardKind_SCHEME {
			t.Fatal("schemes must close the base flow")
		}
		s, _ = play(t, s, r, "scheme_0")
	}
	if s.Phase != pb.Phase_FINISHED || s.CompletedTurns != 11 || len(s.Tools) != 1 {
		t.Fatal("low-score run should finish without fabricating score rewards")
	}
}

func TestSeedStreamReferenceAndScoreOverflow(t *testing.T) {
	var state uint64
	if x := nextRandom(&state); x != 0xe220a8397b1dcdaf {
		t.Fatalf("RNG algorithm drift: %x", x)
	}
	if x := nextRandom(&state); x != 0x6e789e6aa1b965f4 {
		t.Fatalf("RNG algorithm drift: %x", x)
	}
	s, _ := fixture(t)
	put(s, 0, 1, 1, math.MaxInt64, 2)
	if updateRewards(s, false) == nil {
		t.Fatal("score multiplication overflow accepted")
	}
	put(s, 0, 1, 1, math.MaxInt64, 1)
	put(s, 1, 1, 1, 1, 1)
	if updateRewards(s, false) == nil {
		t.Fatal("score sum overflow accepted")
	}
}
