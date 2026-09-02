package ai

import "strings"

const (
	familyBone     = int32(1)
	familyFiend    = int32(2)
	familyAwakener = int32(3)
	familyInsect   = int32(4)
	rarityNormal   = int32(1)
	rarityRare     = int32(3)
	rarityBoss     = int32(4)
)

// playbookPreference 是训练攻略在 Go 推演器中的最小等价表示。
// openingTool 一旦进入已拥有用具列表，就成为整局稳定的流派锁。
type playbookPreference struct {
	key, openingTool string
	coreTools        map[string]bool
	fallbackTools    map[string]bool
	setupPotions     map[string]bool
	buffPotions      map[string]bool
	corePotions      map[string]bool
	alwaysPick       map[string]bool
}

func ids(values ...string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

var searchPlaybooks = []*playbookPreference{
	{key: "insect_pupa", openingTool: "pupa", coreTools: ids("scraper", "tanned_restraint"),
		setupPotions: ids("insect_powder", "brood_hormone", "insect_twin_hormone", "eggshell_powder"),
		buffPotions:  ids("pure_leech", "mixed_leech", "sticky_bile", "awakening"), alwaysPick: ids("insect_boost", "lure")},
	{key: "awakener_boss", openingTool: "pituitary", coreTools: ids("fang", "saw"), fallbackTools: ids("cortex"),
		corePotions: ids("strong_will_powder", "will_powder", "awakening", "pia_mater")},
	{key: "awakener_double", openingTool: "frontal_lobe", coreTools: ids("fang", "rawhide_restraint"),
		corePotions: ids("awaker_fluid", "awaker_anesthetic", "awakening", "mutagen_powder", "mutation")},
	{key: "fiend_growth", openingTool: "growth", coreTools: ids("nettle", "fang", "goat_suture", "marrow"),
		setupPotions: ids("alien_hormone", "insect_powder", "brood_hormone"),
		buffPotions:  ids("awakening", "fiend_anesthetic", "petrified_marrow", "gray_marrow", "hollow_marrow", "proliferation_powder", "mutagen_powder"),
		corePotions:  ids("fresh_marrow_powder", "fiend_anesthetic")},
	{key: "fiend_double", openingTool: "liver", coreTools: ids("nettle", "fang", "saw"),
		setupPotions: ids("alien_hormone", "insect_powder", "brood_hormone"),
		buffPotions:  ids("awakening", "fiend_anesthetic", "petrified_marrow", "gray_marrow", "hollow_marrow", "proliferation_powder", "mutagen_powder"),
		corePotions:  ids("fresh_marrow_powder", "mutagen_powder", "gray_marrow", "mutation")},
	{key: "bone_claw", openingTool: "claw", coreTools: ids("cortex", "goat_suture"), fallbackTools: ids("saw", "scraper", "eye"),
		setupPotions: ids("bone_ointment", "alien_hormone", "targeted_alien_hormone", "will_powder"),
		buffPotions:  ids("awakening", "cleansing_ointment", "mixed_leech", "sticky_bile", "mutation", "awaker_anesthetic", "digestive", "fusion", "petrified_marrow", "gray_marrow", "hollow_marrow")},
	{key: "bone_metatarsal", openingTool: "metatarsal", coreTools: ids("sinew", "scraper"), fallbackTools: ids("tanned_restraint", "cortex"),
		setupPotions: ids("bone_ointment", "alien_hormone", "targeted_alien_hormone", "will_powder"),
		buffPotions:  ids("awakening", "cleansing_ointment", "mixed_leech", "sticky_bile", "mutation", "awaker_anesthetic", "digestive", "fusion", "petrified_marrow", "gray_marrow", "hollow_marrow")},
}

func lockedPlaybook(o *Observation) *playbookPreference {
	// 已拥有的开局核心用具是无需额外状态、可跨 HTTP 请求稳定恢复的流派锁。
	for _, tool := range o.Tools {
		for _, playbook := range searchPlaybooks {
			if tool == playbook.openingTool {
				return playbook
			}
		}
	}
	return selectInitialPlaybook(o)
}

func selectInitialPlaybook(o *Observation) *playbookPreference {
	counts := map[int32]int{familyBone: 0, familyFiend: 0, familyAwakener: 0, familyInsect: 0}
	total, normals, rareAwakeners, otherRares := 0, 0, 0, 0
	for _, slot := range o.Slots {
		if slot.Quantity <= 0 {
			continue
		}
		total++
		counts[slot.Family]++
		if slot.Rarity == rarityNormal {
			normals++
		}
		if slot.Rarity == rarityRare || slot.Rarity == rarityBoss {
			if slot.Family == familyAwakener {
				rareAwakeners++
			} else {
				otherRares++
			}
		}
	}
	if total == 0 {
		return nil
	}
	dominant := familyBone
	for family := familyBone; family <= familyInsect; family++ {
		if counts[family] > counts[dominant] {
			dominant = family
		}
	}
	type candidate struct {
		score int
		book  *playbookPreference
	}
	candidates := []candidate{}
	add := func(score int, book *playbookPreference) {
		candidates = append(candidates, candidate{score, book})
	}
	book := func(key string) *playbookPreference {
		for _, current := range searchPlaybooks {
			if current.key == key {
				return current
			}
		}
		return nil
	}
	if counts[familyInsect] > 0 {
		score := 60 + counts[familyInsect]*8
		if dominant == familyInsect {
			score += 35
		}
		add(score, book("insect_pupa"))
	}
	if rareAwakeners > 0 && total <= 3 {
		add(105+rareAwakeners*5, book("awakener_boss"))
	}
	if counts[familyAwakener] >= 2 || counts[familyAwakener] >= 1 && otherRares >= 1 {
		score := 100 + counts[familyAwakener]*5
		if dominant == familyAwakener {
			score += 30
		}
		add(score, book("awakener_double"))
	}
	if counts[familyFiend] >= 1 && total >= 4 && normals*2 >= total {
		score := 90 + normals*4
		if dominant == familyFiend {
			score += 25
		}
		add(score, book("fiend_growth"))
	}
	if counts[familyFiend] >= 2 && total >= 4 {
		score := 110 + counts[familyFiend]*5
		if dominant == familyFiend {
			score += 25
		}
		add(score, book("fiend_double"))
	}
	if counts[familyBone] > 0 {
		score := 65 + counts[familyBone]*5
		if dominant == familyBone {
			score += 30
		}
		add(score, book("bone_claw"))
	}
	if counts[familyBone] >= 2 {
		score := 110 + counts[familyBone]*5
		if dominant == familyBone {
			score += 30
		}
		add(score, book("bone_metatarsal"))
	}
	if len(candidates) == 0 {
		return nil
	}
	best := candidates[0]
	for _, current := range candidates[1:] {
		if current.score > best.score {
			best = current
		}
	}
	return best.book
}

// lockedOpeningAction 在开局用具阶段落实流派锁：阵容先确定攻略流派，候选不会反向
// 改变流派；目标用具出现就立即选，否则优先使用 pet 提供的用具刷新继续寻找。
func lockedOpeningAction(playbook *playbookPreference, o *Observation, actions []*Action) *Action {
	if playbook == nil || o.Offer.Kind != int32(3) || o.Offer.RewardThreshold != 0 {
		return nil
	}
	for _, action := range actions {
		if action.Type == "choose" && action.CardID == playbook.openingTool {
			return action
		}
	}
	if o.ToolRefreshes > 0 {
		for _, action := range actions {
			if action.Type == "refresh" {
				return action
			}
		}
	}
	return nil
}

func guideCardPoints(playbook *playbookPreference, o *Observation, action *Action) float64 {
	if playbook == nil {
		return 0
	}
	preferred := func(id string) bool {
		return id == playbook.openingTool || playbook.coreTools[id] || playbook.fallbackTools[id] ||
			playbook.setupPotions[id] || playbook.buffPotions[id] || playbook.corePotions[id] || playbook.alwaysPick[id]
	}
	if action.Type == "refresh" {
		return refreshGuidePoints(playbook, o)
	}
	if action.Type != "choose" {
		return 0
	}
	id := action.CardID
	switch {
	case id == playbook.openingTool:
		return 4
	case playbook.alwaysPick[id]:
		return 3
	case playbook.coreTools[id], playbook.corePotions[id]:
		return 2.5
	case playbook.fallbackTools[id]:
		return 1.5
	case playbook.setupPotions[id]:
		if o.BaseCursor <= 4 {
			return 2
		}
		return 1
	case playbook.buffPotions[id]:
		if o.BaseCursor <= 4 {
			return 1
		}
		return 2
	}
	for _, card := range o.Cards {
		if card.Playable && preferred(card.ID) {
			return -0.5
		}
	}
	return 0
}

func refreshGuidePoints(playbook *playbookPreference, o *Observation) float64 {
	const potionKind, toolKind = int32(2), int32(3)
	offered := func(wanted map[string]bool) bool {
		for _, card := range o.Cards {
			if card.Playable && wanted[card.ID] {
				return true
			}
		}
		return false
	}
	switch o.Offer.Kind {
	case toolKind:
		if o.ToolRefreshes <= 0 {
			return 0
		}
		if o.BaseCursor == 0 {
			if offered(ids(playbook.openingTool)) {
				return -1
			}
			// pet=2 的最高价值是开局主动寻找锁流派核心用具。
			return 3
		}
		wanted := ids()
		for id := range playbook.coreTools {
			wanted[id] = true
		}
		for id := range playbook.fallbackTools {
			wanted[id] = true
		}
		if offered(wanted) {
			return -1
		}
		return 1.75 + 0.25*float64(o.ToolRefreshes)
	case potionKind:
		if o.PotionRefreshes <= 0 {
			return 0
		}
		wanted := ids()
		for id := range playbook.corePotions {
			wanted[id] = true
		}
		for id := range playbook.alwaysPick {
			wanted[id] = true
		}
		if o.BaseCursor <= 4 {
			for id := range playbook.setupPotions {
				wanted[id] = true
			}
		} else {
			for id := range playbook.buffPotions {
				wanted[id] = true
			}
		}
		if offered(wanted) {
			return -1
		}
		// 前四瓶用于建立复利基础；剩余机会越少，未使用刷新越不能浪费。
		opportunities := max(1, 9-int(o.BaseCursor))
		urgency := min(1, float64(o.PotionRefreshes)/float64(opportunities))
		early := 0.0
		if o.BaseCursor <= 4 {
			early = 0.75
		}
		return 0.75 + early + 1.25*urgency
	default:
		return 0
	}
}

func guideAdvantages(playbook *playbookPreference, o *Observation, actions []*Action) []float64 {
	result := make([]float64, len(actions))
	if playbook == nil || len(actions) < 2 {
		return result
	}
	low, high, sum := 1e9, -1e9, 0.0
	for i, action := range actions {
		result[i] = guideCardPoints(playbook, o, action)
		low = min(low, result[i])
		high = max(high, result[i])
		sum += result[i]
	}
	span := high - low
	if span <= 1e-9 {
		return make([]float64, len(actions))
	}
	mean := sum / float64(len(actions))
	for i := range result {
		result[i] = max(-1, min(1, (result[i]-mean)/span))
	}
	return result
}

func playbookName(playbook *playbookPreference) string {
	if playbook == nil {
		return "unmatched"
	}
	return strings.ReplaceAll(playbook.key, "_", "-")
}
