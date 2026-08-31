package engine

import (
	"testing"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

func TestGrayMarrowAnimationSnapshots(t *testing.T) {
	branches := map[int]bool{}
	for seed := uint64(1); seed < 80; seed++ {
		s, r := fixture(t)
		put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
		s.EffectRng = seed
		c := potionTestPlay(t, s, r, "gray_marrow")
		branches[len(c.events)] = true
		want := []string{"stats_changed", "mutated"}
		if len(c.events) == 4 {
			want = append(want, "mutated", "stats_changed")
		}
		if len(c.events) != len(want) {
			t.Fatalf("unexpected sequence: %v", c.events)
		}
		for i, event := range c.events {
			if event.Kind != want[i] || len(event.SlotsAfter) != 6 || event.SourceName != r.Card("gray_marrow").Name {
				t.Fatalf("invalid snapshot %d: %v", i, event)
			}
		}
		first := c.events[0].SlotsAfter[0].Monster
		if first.Activity != 51 || first.Quantity != 20 || first.Family != pb.Family_BONE || first.Rarity != pb.MonsterRarity_NORMAL {
			t.Fatalf("first buff includes a later transformation: %v", first)
		}
		for _, event := range c.events {
			if event.Kind == "mutated" {
				baseA, baseQ := base(event.SlotsAfter[0].Monster.Rarity)
				if event.ActivityDelta != baseA || event.QuantityDelta != baseQ {
					t.Fatal("transformation snapshot has wrong destination")
				}
			}
		}
		last := c.events[len(c.events)-1].SlotsAfter[0].Monster
		if !proto.Equal(last, s.Slots[0].Monster) {
			t.Fatal("last snapshot differs from final monster")
		}
		if len(c.events) == 4 {
			secondMutation := c.events[2].SlotsAfter[0].Monster
			if secondMutation.Family != pb.Family_FIEND || last.Activity-secondMutation.Activity != 30 {
				t.Fatal("second mutation and final buff are not separate")
			}
		}
		s.Slots[0].Monster.Activity++
		if proto.Equal(last, s.Slots[0].Monster) {
			t.Fatal("snapshot shares mutable state")
		}
	}
	if !branches[2] || !branches[4] {
		t.Fatal("both probability branches must be covered")
	}
}

func TestAnimationSnapshotsPrecedeNestedTools(t *testing.T) {
	s, r := fixture(t)
	s.Tools = []string{"growth"}
	id := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	c := &context{state: s, rules: r, source: "mutation", limit: 512}
	c.transform(id, pb.Family_FIEND, pb.MonsterRarity_MAGIC, false, 0)
	if c.err != nil || len(c.events) != 2 {
		t.Fatalf("unexpected chain: %v %v", c.err, c.events)
	}
	parent, child := c.events[0], c.events[1]
	if parent.SlotsAfter[0].Monster.Activity != 15 || child.SlotsAfter[0].Monster.Activity != 50 || child.ParentSequence != parent.Sequence || child.SourceName != r.Card("growth").Name {
		t.Fatal("nested tool buff leaked into transformation snapshot")
	}
	child.SlotsAfter[0].Monster.Activity = 999
	if parent.SlotsAfter[0].Monster.Activity != 15 || s.Slots[0].Monster.Activity != 50 {
		t.Fatal("snapshots are not isolated")
	}
}

func TestStructuralAnimationSnapshots(t *testing.T) {
	s, r := fixture(t)
	first := put(s, 0, pb.Family_BONE, pb.MonsterRarity_NORMAL, 10, 20)
	second := put(s, 3, pb.Family_INSECT, pb.MonsterRarity_MAGIC, 30, 40)
	c := &context{state: s, rules: r, limit: 512}
	c.transform(first, 0, 0, true, 1)
	c.fuse([]string{second, first}, pb.Family_AWAKENER, pb.MonsterRarity_RARE)
	fused := s.Slots[0].Monster.Id
	c.remove(fused)
	c.add(pb.Family_FIEND, pb.MonsterRarity_BOSS, 0, 0, &s.EffectRng)
	if c.err != nil || len(c.events) != 4 {
		t.Fatalf("unexpected events: %v %v", c.err, c.events)
	}
	if c.events[0].Kind != "awakened" || c.events[0].SlotsAfter[0].Monster.Rarity != pb.MonsterRarity_MAGIC {
		t.Fatal("missing awakening snapshot")
	}
	if c.events[1].Kind != "fused" || c.events[1].SlotsAfter[3].Monster != nil || c.events[1].SlotsAfter[0].Monster.Id != fused {
		t.Fatal("fusion destination or consumed slot missing")
	}
	if c.events[2].Kind != "removed" || c.events[2].SlotsAfter[0].Monster != nil {
		t.Fatal("removed slot is not empty")
	}
	if c.events[3].Kind != "added" || c.events[3].SlotsAfter[0].Monster.Family != pb.Family_FIEND {
		t.Fatal("added monster missing")
	}
}
