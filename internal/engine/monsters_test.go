package engine

import (
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func assertNamedMonster(t *testing.T, m *pb.Monster) {
	t.Helper()
	d := findMonsterDefinition(m.DefinitionId)
	if d == nil || m.Name != d.name || m.Family != d.family || m.Rarity != d.rarity {
		t.Fatalf("invalid monster identity: %v", m)
	}
}

func TestMonsterCatalog(t *testing.T) {
	manual, err := os.ReadFile("../../渴瘾玩法说明书.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(monsterDefinitions) != 30 {
		t.Fatalf("monster count = %d", len(monsterDefinitions))
	}
	names, ids := map[string]bool{}, map[string]bool{}
	families := [4]int{}
	rarities := [4]int{}
	stats := [4][2]int64{{1, 36}, {5, 24}, {15, 12}, {300, 1}}
	for _, d := range monsterDefinitions {
		if names[d.name] || ids[d.id] || d.id == "" || d.name == "" || !strings.Contains(string(manual), d.name) {
			t.Fatalf("invalid or undocumented definition: %+v", d)
		}
		names[d.name], ids[d.id] = true, true
		families[d.family-1]++
		rarities[d.rarity-1]++
		a, q := base(d.rarity)
		if [2]int64{a, q} != stats[d.rarity-1] {
			t.Fatalf("base stats for %s = %d x %d", d.name, a, q)
		}
	}
	if families != [4]int{7, 7, 8, 8} || rarities != [4]int{9, 8, 7, 6} {
		t.Fatalf("classification counts = %v / %v", families, rarities)
	}
}

func TestNamedMonsterGeneration(t *testing.T) {
	seen := map[string]bool{}
	for f := pb.Family_BONE; f <= pb.Family_INSECT; f++ {
		for r := pb.MonsterRarity_NORMAL; r <= pb.MonsterRarity_BOSS; r++ {
			for seed := uint64(0); seed < 64; seed++ {
				s, rules := fixture(t)
				c := &context{state: s, rules: rules, limit: 512}
				s.InitRng = seed
				id := c.add(f, r, 7, 9, &s.InitRng)
				m := getMonster(s, id)
				if c.err != nil || m == nil {
					t.Fatalf("generation failed: %v", c.err)
				}
				assertNamedMonster(t, m)
				a, q := base(r)
				if m.Family != f || m.Rarity != r || m.Activity != a+7 || m.Quantity != q+9 {
					t.Fatalf("generation changed requested stats: %v", m)
				}
				if !proto.Equal(c.events[0].SlotsAfter[0].Monster, m) {
					t.Fatal("added snapshot lost identity")
				}
				seen[m.DefinitionId] = true
			}
		}
	}
	if len(seen) != len(monsterDefinitions) {
		t.Fatalf("generated %d of %d definitions", len(seen), len(monsterDefinitions))
	}
}

func TestNamedMonsterRandomClassification(t *testing.T) {
	for seed := uint64(0); seed < 256; seed++ {
		s, rules := fixture(t)
		c := &context{state: s, rules: rules, limit: 512}
		s.InitRng = seed
		rng := seed
		for i := 0; i < 6; i++ {
			f := pb.Family(1 + randomN(&rng, 4))
			r := monsterRarityAt(randomN(&rng, 100))
			d := pickMonsterDefinition(f, r, &rng)
			m := getMonster(s, c.add(0, 0, 0, 0, &s.InitRng))
			if c.err != nil || m == nil || m.Family != f || m.Rarity != r || m.DefinitionId != d.id || s.InitRng != rng {
				t.Fatalf("random classification mismatch: %v / %v", m, c.err)
			}
		}
	}
}

func TestNamedMonsterCopyEffects(t *testing.T) {
	for _, d := range monsterDefinitions {
		t.Run(d.id, func(t *testing.T) {
			s, rules := fixture(t)
			id := put(s, 0, d.family, d.rarity, 100, 200)
			d.identify(s.Slots[0].Monster)
			right := put(s, 3, pb.Family_BONE, pb.MonsterRarity_NORMAL, 20, 30)
			c := potionTestPlay(t, s, rules, "sticky_bile", id)
			m := getMonster(s, right)
			a, q := base(d.rarity)
			if m.DefinitionId != d.id || m.Name != d.name || m.Activity != 20+a+31 || m.Quantity != 30+q {
				t.Fatalf("sticky bile did not copy definition: %v", m)
			}
			assertNamedMonster(t, c.events[0].SlotsAfter[3].Monster)
			if d.family != pb.Family_INSECT {
				return
			}
			s.Slots[3].Monster = nil
			c = potionTestPlay(t, s, rules, "eggshell_powder")
			m = s.Slots[1].Monster
			if m == nil || m.DefinitionId != d.id || m.Name != d.name || m.Activity != a || m.Quantity != q+25 {
				t.Fatalf("eggshell did not add same name with base stats: %v", m)
			}
			for _, event := range c.events {
				if event.Kind == "added" && !proto.Equal(event.SlotsAfter[1].Monster, m) {
					t.Fatal("copy snapshot lost identity")
				}
			}
		})
	}
}

func TestNamedMonsterTransformAndFusion(t *testing.T) {
	s, rules := fixture(t)
	c := &context{state: s, rules: rules, limit: 512}
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 100, 200)
	c.transform(id, 0, 0, true, 1)
	m := getMonster(s, id)
	assertNamedMonster(t, m)
	if m.Family != pb.Family_BONE || m.Rarity != pb.MonsterRarity_MAGIC || m.Activity != 105 || m.Quantity != 224 {
		t.Fatalf("awakening: %v", m)
	}
	c.transform(id, pb.Family_FIEND, pb.MonsterRarity_RARE, false, 0)
	assertNamedMonster(t, m)
	if m.Family != pb.Family_FIEND || m.Rarity != pb.MonsterRarity_RARE || m.Activity != 120 || m.Quantity != 236 {
		t.Fatalf("mutation: %v", m)
	}
	other := put(s, 5, pb.Family_INSECT, pb.MonsterRarity_MAGIC, 10, 20)
	c.fuse([]string{id, other}, pb.Family_INSECT, 0)
	m = s.Slots[0].Monster
	assertNamedMonster(t, m)
	if m.Family != pb.Family_INSECT || m.Rarity != pb.MonsterRarity_BOSS || m.Activity != 130 || m.Quantity != 256 {
		t.Fatalf("fusion: %v", m)
	}
	definitionID, name := m.DefinitionId, m.Name
	other = put(s, 5, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	c.devour(m.Id, other)
	if m.DefinitionId != definitionID || m.Name != name || m.Activity != 140 || m.Quantity != 276 || c.err != nil {
		t.Fatalf("devour: %v / %v", m, c.err)
	}
}

func TestHolyWaterSamplesNamedBosses(t *testing.T) {
	seen := map[string]bool{}
	for seed := uint64(0); seed < 64; seed++ {
		s, rules := fixture(t)
		s.EffectRng = seed
		id := put(s, 0, pb.Family_AWAKENER, pb.MonsterRarity_BOSS, 300, 1)
		c := potionTestPlay(t, s, rules, "holy_water")
		m := getMonster(s, id)
		assertNamedMonster(t, m)
		seen[m.DefinitionId] = true
		if m.Activity != 600 || m.Quantity != 2 || m.Rarity != pb.MonsterRarity_BOSS || m.Family != pb.Family_AWAKENER || eventCount(c.events, "mutated") != 0 {
			t.Fatalf("boss reroll: %v", m)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("boss variants = %v", seen)
	}
}
