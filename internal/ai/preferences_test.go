package ai

import "testing"

func TestOpeningToolLocksPlaybook(t *testing.T) {
	o := &Observation{Tools: []string{"pupa"}, Slots: []SlotView{{Index: 0, Family: familyBone, Rarity: rarityBoss, Activity: 300, Quantity: 100}}}
	if got := playbookName(lockedPlaybook(o)); got != "insect-pupa" {
		t.Fatalf("locked playbook = %q, want insect-pupa", got)
	}
}

func TestInitialPlaybookDoesNotDriftWithOpeningOffer(t *testing.T) {
	slots := []SlotView{
		{Index: 0, Family: familyAwakener, Rarity: rarityRare, Activity: 15, Quantity: 12},
		{Index: 1, Family: familyAwakener, Rarity: rarityNormal, Activity: 1, Quantity: 36},
	}
	a := &Observation{Slots: slots, Cards: []CardView{{ID: "claw", Playable: true}}}
	b := &Observation{Slots: slots, Cards: []CardView{{ID: "frontal_lobe", Playable: true}}}
	if playbookName(selectInitialPlaybook(a)) != playbookName(selectInitialPlaybook(b)) {
		t.Fatal("开局候选变化不应改变由初始阵容确定的流派")
	}
}

func TestLockedOpeningActionUsesPetRefreshThenTakesTarget(t *testing.T) {
	book := searchPlaybooks[0]
	missing := &Observation{ToolRefreshes: 2, Offer: OfferView{Kind: 3, RewardThreshold: 0}}
	missingActions := []*Action{{Type: "refresh"}, {Type: "choose", CardID: "claw"}}
	if got := lockedOpeningAction(book, missing, missingActions); got == nil || got.Type != "refresh" {
		t.Fatalf("目标开局用具缺失时应优先使用 pet 刷新，得到 %v", got)
	}

	hit := &Observation{ToolRefreshes: 1, Offer: OfferView{Kind: 3, RewardThreshold: 0}}
	hitActions := []*Action{{Type: "refresh"}, {Type: "choose", CardID: "pupa"}}
	if got := lockedOpeningAction(book, hit, hitActions); got == nil || got.Type != "choose" || got.CardID != "pupa" {
		t.Fatalf("目标开局用具出现时应立即锁定，得到 %v", got)
	}
}

func TestGuidePrefersBuildPotionAndSafeRefreshBehavior(t *testing.T) {
	book := searchPlaybooks[0]
	o := &Observation{BaseCursor: 2, PotionRefreshes: 3, Offer: OfferView{Kind: 2}, Cards: []CardView{{ID: "insect_powder", Playable: true}}}
	preferred := &Action{Type: "choose", CardID: "insect_powder"}
	offBuild := &Action{Type: "choose", CardID: "bone_ointment"}
	refresh := &Action{Type: "refresh"}
	if guideCardPoints(book, o, preferred) <= guideCardPoints(book, o, offBuild) {
		t.Fatal("build potion should outrank off-build potion")
	}
	if guideCardPoints(book, o, refresh) >= 0 {
		t.Fatal("refresh should be discouraged while a preferred card is offered")
	}
}

func TestGuideAdvantagesAreCenteredAndRanked(t *testing.T) {
	book := searchPlaybooks[0]
	o := &Observation{BaseCursor: 2, Cards: []CardView{{ID: "insect_powder", Playable: true}}}
	actions := []*Action{{Type: "choose", CardID: "insect_powder"}, {Type: "choose", CardID: "bone_ointment"}}
	advantages := guideAdvantages(book, o, actions)
	if advantages[0] <= 0 || advantages[1] >= 0 {
		t.Fatalf("unexpected guide advantages: %v", advantages)
	}
}

func TestRefreshStronglySeeksOpeningToolAndEarlySetupPotion(t *testing.T) {
	book := searchPlaybooks[0]
	openingMiss := &Observation{BaseCursor: 0, ToolRefreshes: 2, Offer: OfferView{Kind: 3}, Cards: []CardView{{ID: "claw", Playable: true}}}
	openingHit := &Observation{BaseCursor: 0, ToolRefreshes: 2, Offer: OfferView{Kind: 3}, Cards: []CardView{{ID: "pupa", Playable: true}}}
	if refreshGuidePoints(book, openingMiss) <= 2 || refreshGuidePoints(book, openingHit) >= 0 {
		t.Fatalf("unexpected opening refresh scores: miss=%v hit=%v", refreshGuidePoints(book, openingMiss), refreshGuidePoints(book, openingHit))
	}

	earlyMiss := &Observation{BaseCursor: 2, PotionRefreshes: 3, Offer: OfferView{Kind: 2}, Cards: []CardView{{ID: "bone_ointment", Playable: true}}}
	earlyHit := &Observation{BaseCursor: 2, PotionRefreshes: 3, Offer: OfferView{Kind: 2}, Cards: []CardView{{ID: "insect_powder", Playable: true}}}
	if refreshGuidePoints(book, earlyMiss) <= 1 || refreshGuidePoints(book, earlyHit) >= 0 {
		t.Fatalf("unexpected potion refresh scores: miss=%v hit=%v", refreshGuidePoints(book, earlyMiss), refreshGuidePoints(book, earlyHit))
	}
}
