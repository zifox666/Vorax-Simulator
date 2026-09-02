package engine

import (
	"fmt"

	pb "vorax/internal/protocol"
)

func potionCards() []*pb.CardDefinition {
	white, blue, gold, red := pb.PotionRarity_WHITE, pb.PotionRarity_BLUE, pb.PotionRarity_GOLD, pb.PotionRarity_RED
	cards := []*pb.CardDefinition{
		{Id: "bone_ointment", Name: "化骨油膏", Description: "至少拥有1组骨卫兵时，随机移除至多3组非骨卫兵怪物，每移除1组，使所有怪物+28活性+28数量", Rarity: gold, Handler: "potion_bone_ointment", Enabled: true},
		{Id: "alien_hormone", Name: "异种激素", Description: "选择1-2组怪物移除，添加4组随机怪物，使其+5活性", Rarity: white, MinTargets: 1, MaxTargets: 2, Handler: "potion_alien_hormone", Enabled: true},
		{Id: "targeted_alien_hormone", Name: "靶向异种激素", Description: "选择1组怪物移除，添加2组随机种群的魔法怪物，并使其+25数量", Rarity: white, MinTargets: 1, MaxTargets: 1, Handler: "potion_targeted_alien_hormone", Enabled: true},
		{Id: "will_powder", Name: "祛异药粉", Description: "选择1组怪物+127数量，移除1组与其不同种群的怪物", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_will_powder"},
		{Id: "awakening", Name: "迷魂酊剂", Description: "选择1组怪物，觉醒为高一阶稀有度的怪物", Rarity: blue, MinTargets: 1, MaxTargets: 1, Effects: []*pb.EffectSpec{{Op: "awaken", Selector: "selected", Count: 1}}, Enabled: true},
		{Id: "cleansing_ointment", Name: "清疽油膏", Description: "选择1组怪物+20活性；如果是骨卫兵额外+41数量并移除其右侧怪物", Rarity: gold, MinTargets: 1, MaxTargets: 1, Handler: "potion_cleansing_ointment", Enabled: true},
		{Id: "mixed_leech", Name: "混合活蛭溶液", Description: "选择1组怪物，使得2组稀有度相同的怪物+31活性", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_mixed_leech"},
		{Id: "sticky_bile", Name: "黏稠胆汁溶液", Description: "选择1组怪物，将其右侧怪物变异为与其相同的怪物，两者各+31活性", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_sticky_bile", Enabled: true},
		{Id: "mutation", Name: "青胆汁溶液", Description: "选择1-2组怪物，将其变异为随机魔法怪物，并+31活性", Rarity: blue, MinTargets: 1, MaxTargets: 2, Effects: []*pb.EffectSpec{{Op: "mutate", Selector: "selected", Rarity: pb.MonsterRarity_MAGIC}, {Op: "buff", Selector: "selected", Activity: 31}}, Enabled: true},
		{Id: "awaker_anesthetic", Name: "麻药酊剂-觉醒者", Description: "选择1组怪物，变异为觉醒者，觉醒为魔法怪物", Rarity: white, MinTargets: 1, MaxTargets: 1, Handler: "potion_awaker_anesthetic"},
		{Id: "digestive", Name: "消化酶溶液", Description: "选择1组怪物+61活性，移除其左侧怪物", Rarity: blue, MinTargets: 1, MaxTargets: 1, Effects: []*pb.EffectSpec{{Op: "buff", Selector: "selected", Activity: 61}, {Op: "remove_left", Selector: "selected"}}, Enabled: true},
		{Id: "fusion", Name: "蜕生皮溶液", Description: "选择1-2组怪物融合为1组蛊虫", Rarity: gold, MinTargets: 1, MaxTargets: 2, Handler: "potion_fusion", Enabled: true},
		{Id: "petrified_marrow", Name: "石化脊髓溶液", Description: "选择1组怪物+20活性，如果不算异魔则变异为异魔，并使其额外+30活性", Rarity: gold, MinTargets: 1, MaxTargets: 1, Handler: "potion_petrified_marrow", Enabled: true},
		{Id: "gray_marrow", Name: "灰质脊髓溶液", Description: "随机1组怪物+41活性，变异为随机怪物；有50%概率将其再次变异为异魔并+30活性", Rarity: blue, Handler: "potion_gray_marrow", Enabled: true},
		{Id: "hollow_marrow", Name: "空心脊髓溶液", Description: "选择1组怪物+41活性，50%概率将其变异为随机异魔", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_hollow_marrow", Enabled: true},
		{Id: "strong_will_powder", Name: "强效祛异药粉", Description: "选择1组怪物+154数量，移除2组与其不同种群的怪物", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_strong_will_powder"},
		{Id: "pia_mater", Name: "软脑膜溶液", Description: "选择1组怪物+30活性、魔法觉醒者生效2次、稀有/首领觉醒者生效3次", Rarity: gold, MinTargets: 1, MaxTargets: 1, Handler: "potion_pia_mater", Enabled: true},
		{Id: "awaker_fluid", Name: "脊髓溶液-觉醒者", Description: "添加1组觉醒者，使其+31活性", Rarity: white, Effects: []*pb.EffectSpec{{Op: "add", Count: 1, Family: pb.Family_AWAKENER, Activity: 31}}, Enabled: true},
		{Id: "mutagen_powder", Name: "诱变药粉", Description: "选择1-2组怪物+52数量，使其变异为随机稀有怪物", Rarity: blue, MinTargets: 1, MaxTargets: 2, Effects: []*pb.EffectSpec{{Op: "buff", Selector: "selected", Quantity: 52}, {Op: "mutate", Selector: "selected", Rarity: pb.MonsterRarity_RARE}}, Enabled: true},
		{Id: "holy_water", Name: "至纯圣水", Description: "所有觉醒者觉醒为高2阶稀有度的怪物，如果觉醒前稀有度为首领则变为随机首领觉醒者", Rarity: red, Handler: "potion_holy_water"},
		{Id: "peat_dressing", Name: "疫区泥炭敷料", Description: "至少拥有1组骨卫兵时，移除所有非骨卫兵怪物，每移除1组怪物，使所有骨卫兵+20活性+20数量", Rarity: red, Handler: "potion_peat_dressing", Enabled: true},
		{Id: "waking_salts", Name: "惊醒嗅盐", Description: "随机选择1组怪物，使其+111活性、+111数量", Rarity: red, Handler: "potion_waking_salts", Enabled: true},
		{Id: "box_3", Name: "稀有药剂箱(小)", Description: "包含3支随机药剂，稀有和至臻药剂概率翻倍", Rarity: gold, Handler: "potion_box_3"},
		{Id: "box_5", Name: "稀有药剂箱(大)", Description: "包含5支随机药剂，稀有和至臻药剂概率翻倍", Rarity: gold, Handler: "potion_box_5"},
		{Id: "normal_box_3", Name: "药剂箱(小)", Description: "包含3支随机药剂", Rarity: gold, Handler: "potion_normal_box_3"},
		{Id: "normal_box_5", Name: "药剂箱(大)", Description: "包含5支随机药剂", Rarity: gold, Handler: "potion_normal_box_5"},
		{Id: "fresh_marrow_powder", Name: "鲜脊髓药粉", Description: "随机至多3组非异魔变异为异魔，每变异1组，使随机3组异魔+42数量", Rarity: gold, Handler: "potion_fresh_marrow_powder"},
		{Id: "fiend_anesthetic", Name: "麻药酊剂-异魔", Description: "选择1组怪物，变异为异魔，50%概率再随机1组怪物变异为异魔", Rarity: white, MinTargets: 1, MaxTargets: 1, Handler: "potion_fiend_anesthetic", Enabled: true},
		{Id: "insect_powder", Name: "细肢药粉-蛊虫", Description: "添加1组蛊虫，使其+73数量", Rarity: white, Effects: []*pb.EffectSpec{{Op: "add", Count: 1, Family: pb.Family_INSECT, Quantity: 73}}, Enabled: true},
		{Id: "bone_powder", Name: "细肢药粉-骨卫兵", Description: "添加1组骨卫兵，使其+73数量", Rarity: white, Effects: []*pb.EffectSpec{{Op: "add", Count: 1, Family: pb.Family_BONE, Quantity: 73}}, Enabled: true},
		{Id: "brood_hormone", Name: "活性育卵激素", Description: "添加1组蛊虫，50%概率额外添加2组", Rarity: blue, Handler: "potion_brood_hormone", Enabled: true},
		{Id: "proliferation_powder", Name: "活殖药粉", Description: "随机4组稀有度各不相同的怪物+32数量", Rarity: blue, Handler: "potion_proliferation_powder"},
		{Id: "eggshell_powder", Name: "卵壳药粉", Description: "随机1组怪物+25数量，如果是蛊虫，添加1组同名蛊虫并为其+25数量", Rarity: blue, Handler: "potion_eggshell_powder"},
		{Id: "pure_leech", Name: "纯粹活蛭溶液", Description: "选择1组怪物，使2组同种群怪物+31活性", Rarity: blue, MinTargets: 1, MaxTargets: 1, Handler: "potion_pure_leech"},
		{Id: "insect_boost", Name: "益生霉溶液", Description: "每有1组蛊虫，所有蛊虫+11活性", Rarity: gold, Handler: "potion_insect_boost", Enabled: true},
		{Id: "lure", Name: "诱虫剂", Description: "添加4组蛊虫，每溢出1组，使已有蛊虫+10活性+10数量", Rarity: red, Handler: "potion_lure", Enabled: true},
		{Id: "brain_fog", Name: "脑雾酊剂", Description: "随机1组怪物+41活性，如果是觉醒者，觉醒为稀有怪物", Rarity: blue, Handler: "potion_brain_fog"},
		{Id: "bone_growth_powder", Name: "生骨药粉", Description: "随机1组怪物+40数量，如果是骨卫兵额外+127数量并移除1组非骨卫兵", Rarity: blue, Handler: "potion_bone_growth_powder"},
		{Id: "fiend_fluid", Name: "脊髓溶液-异魔", Description: "添加1组异魔，使其+31活性", Rarity: white, Effects: []*pb.EffectSpec{{Op: "add", Count: 1, Family: pb.Family_FIEND, Activity: 31}}, Enabled: true},
		{Id: "bone_twin_hormone", Name: "孪生激素-骨卫兵", Description: "选择1组怪物，变异为骨卫兵，使其+62数量", Rarity: white, MinTargets: 1, MaxTargets: 1, Effects: []*pb.EffectSpec{{Op: "mutate", Selector: "selected", Family: pb.Family_BONE}, {Op: "buff", Selector: "selected", Quantity: 62}}, Enabled: true},
		{Id: "insect_twin_hormone", Name: "孪生激素-蛊虫", Description: "选择1组怪物，变异为蛊虫，并添加1组同名怪物", Rarity: white, MinTargets: 1, MaxTargets: 1, Handler: "potion_insect_twin_hormone", Enabled: true},
	}
	for _, card := range cards {
		card.Kind = pb.CardKind_POTION
		card.Enabled = true
	}
	return cards
}

func potionTargetsValid(s *pb.GameState, card *pb.CardDefinition, ids []string) bool {
	if card.Kind != pb.CardKind_POTION {
		return true
	}
	switch card.Handler {
	case "potion_bone_ointment", "potion_peat_dressing":
		for _, slot := range s.Slots {
			if slot.Monster != nil && slot.Monster.Family == pb.Family_BONE {
				return true
			}
		}
		return false
	case "potion_gray_marrow", "potion_brain_fog", "potion_bone_growth_powder", "potion_eggshell_powder", "potion_waking_salts":
		return len(monsterIDs(s)) > 0
	}
	return true
}

func (c *context) potionRandom(ids []string, count int) []string {
	pool := append([]string{}, ids...)
	chosen := []string{}
	for len(pool) > 0 && len(chosen) < count {
		i := randomN(&c.state.EffectRng, len(pool))
		chosen = append(chosen, pool[i])
		pool = append(pool[:i], pool[i+1:]...)
	}
	return chosen
}

func (c *context) potionOtherFamily(f pb.Family) []string {
	ids := []string{}
	for _, slot := range c.state.Slots {
		if slot.Monster != nil && slot.Monster.Family != f {
			ids = append(ids, slot.Monster.Id)
		}
	}
	return ids
}

func (c *context) potionRight(id string) string {
	found := false
	for _, slot := range c.state.Slots {
		if slot.Monster == nil {
			continue
		}
		if found {
			return slot.Monster.Id
		}
		found = slot.Monster.Id == id
	}
	return ""
}

func (c *context) potionBuff(ids []string, activity, quantity int64) {
	for _, id := range ids {
		c.buff(id, activity, quantity)
	}
}

func (c *context) applyPotion(handler string, ids []string) {
	if c.err != nil {
		return
	}
	switch handler {
	case "":
		return
	case "potion_bone_ointment":
		for _, id := range c.potionRandom(c.potionOtherFamily(pb.Family_BONE), 3) {
			c.remove(id)
			c.potionBuff(monsterIDs(c.state), 28, 28)
		}
	case "potion_alien_hormone", "potion_targeted_alien_hormone":
		for _, id := range ids {
			c.remove(id)
		}
		count, activity, quantity := 4, int64(5), int64(0)
		rarity := pb.MonsterRarity(0)
		if handler == "potion_targeted_alien_hormone" {
			count, activity, quantity, rarity = 2, 0, 25, pb.MonsterRarity_MAGIC
		}
		for i := 0; i < count; i++ {
			c.add(0, rarity, activity, quantity, &c.state.EffectRng)
		}
	case "potion_cleansing_ointment":
		m := getMonster(c.state, ids[0])
		bone := m.Family == pb.Family_BONE
		c.buff(m.Id, 20, 0)
		if bone {
			c.buff(m.Id, 0, 41)
			if right := c.potionRight(m.Id); right != "" {
				c.remove(right)
			}
		}
	case "potion_will_powder", "potion_strong_will_powder":
		m := getMonster(c.state, ids[0])
		count, quantity := 1, int64(127)
		if handler == "potion_strong_will_powder" {
			count, quantity = 2, 154
		}
		c.buff(m.Id, 0, quantity)
		for _, id := range c.potionRandom(c.potionOtherFamily(m.Family), count) {
			c.remove(id)
		}
	case "potion_mixed_leech", "potion_pure_leech":
		m := getMonster(c.state, ids[0])
		other := []string{}
		for _, slot := range c.state.Slots {
			n := slot.Monster
			if n == nil || n.Id == m.Id {
				continue
			}
			if handler == "potion_pure_leech" && n.Family == m.Family || handler == "potion_mixed_leech" && n.Rarity == m.Rarity {
				other = append(other, n.Id)
			}
		}
		c.potionBuff(append([]string{m.Id}, c.potionRandom(other, 1)...), 31, 0)
	case "potion_awaker_anesthetic":
		c.transform(ids[0], pb.Family_AWAKENER, 0, false, 0)
		c.transform(ids[0], 0, pb.MonsterRarity_MAGIC, true, 0)
	case "potion_insect_twin_hormone":
		c.transform(ids[0], pb.Family_INSECT, 0, false, 0)
		m := getMonster(c.state, ids[0])
		c.addMonster(m.Family, m.Rarity, 0, 0, &c.state.EffectRng, definitionOf(m))
	case "potion_sticky_bile":
		m := getMonster(c.state, ids[0])
		right := c.potionRight(m.Id)
		if right != "" {
			c.transformTo(right, definitionOf(m), "mutated")
		}
		c.buff(m.Id, 31, 0)
		if right != "" {
			c.buff(right, 31, 0)
		}
	case "potion_fusion":
		c.fuse(ids, pb.Family_INSECT, 0)
	case "potion_petrified_marrow":
		m := getMonster(c.state, ids[0])
		fiend := m.Family == pb.Family_FIEND
		c.buff(m.Id, 20, 0)
		if !fiend {
			c.transform(m.Id, pb.Family_FIEND, 0, false, 0)
			c.buff(m.Id, 30, 0)
		}
	case "potion_gray_marrow":
		for _, id := range c.potionRandom(monsterIDs(c.state), 1) {
			c.buff(id, 41, 0)
			c.transform(id, 0, 0, false, 0)
			if randomN(&c.state.EffectRng, 2) == 0 {
				c.transform(id, pb.Family_FIEND, 0, false, 0)
				c.buff(id, 30, 0)
			}
		}
	case "potion_hollow_marrow":
		c.buff(ids[0], 41, 0)
		if randomN(&c.state.EffectRng, 2) == 0 {
			c.transform(ids[0], pb.Family_FIEND, 0, false, 0)
		}
	case "potion_pia_mater":
		m := getMonster(c.state, ids[0])
		count := 1
		if m.Family == pb.Family_AWAKENER {
			if m.Rarity == pb.MonsterRarity_MAGIC {
				count = 2
			} else if m.Rarity >= pb.MonsterRarity_RARE {
				count = 3
			}
		}
		for i := 0; i < count; i++ {
			c.buff(m.Id, 30, 0)
		}
	case "potion_peat_dressing":
		for _, id := range c.potionOtherFamily(pb.Family_BONE) {
			c.remove(id)
			c.potionBuff(c.family(pb.Family_BONE), 20, 20)
		}
	case "potion_holy_water":
		for _, id := range c.family(pb.Family_AWAKENER) {
			if getMonster(c.state, id).Rarity == pb.MonsterRarity_BOSS {
				c.transform(id, 0, pb.MonsterRarity_BOSS, true, 0)
			} else {
				c.transform(id, 0, 0, true, 2)
			}
		}
	case "potion_waking_salts":
		c.potionBuff(c.potionRandom(monsterIDs(c.state), 1), 111, 111)
	case "potion_fresh_marrow_powder":
		for _, id := range c.potionRandom(c.potionOtherFamily(pb.Family_FIEND), 3) {
			c.transform(id, pb.Family_FIEND, 0, false, 0)
			c.potionBuff(c.potionRandom(c.family(pb.Family_FIEND), 3), 0, 42)
		}
	case "potion_fiend_anesthetic":
		c.transform(ids[0], pb.Family_FIEND, 0, false, 0)
		if randomN(&c.state.EffectRng, 2) == 0 {
			for _, id := range c.potionRandom(monsterIDs(c.state), 1) {
				c.transform(id, pb.Family_FIEND, 0, false, 0)
			}
		}
	case "potion_brood_hormone":
		c.add(pb.Family_INSECT, 0, 0, 0, &c.state.EffectRng)
		if randomN(&c.state.EffectRng, 2) == 0 {
			for i := 0; i < 2; i++ {
				c.add(pb.Family_INSECT, 0, 0, 0, &c.state.EffectRng)
			}
		}
	case "potion_proliferation_powder":
		for rarity := pb.MonsterRarity_NORMAL; rarity <= pb.MonsterRarity_BOSS; rarity++ {
			group := []string{}
			for _, slot := range c.state.Slots {
				if slot.Monster != nil && slot.Monster.Rarity == rarity {
					group = append(group, slot.Monster.Id)
				}
			}
			c.potionBuff(c.potionRandom(group, 1), 0, 32)
		}
	case "potion_eggshell_powder":
		for _, id := range c.potionRandom(monsterIDs(c.state), 1) {
			m := getMonster(c.state, id)
			c.buff(id, 0, 25)
			if m.Family == pb.Family_INSECT {
				c.addMonster(m.Family, m.Rarity, 0, 25, &c.state.EffectRng, definitionOf(m))
			}
		}
	case "potion_brain_fog":
		for _, id := range c.potionRandom(monsterIDs(c.state), 1) {
			c.buff(id, 41, 0)
			if getMonster(c.state, id).Family == pb.Family_AWAKENER {
				c.transform(id, 0, pb.MonsterRarity_RARE, true, 0)
			}
		}
	case "potion_bone_growth_powder":
		for _, id := range c.potionRandom(monsterIDs(c.state), 1) {
			c.buff(id, 0, 40)
			if getMonster(c.state, id).Family == pb.Family_BONE {
				c.buff(id, 0, 127)
				for _, removed := range c.potionRandom(c.potionOtherFamily(pb.Family_BONE), 1) {
					c.remove(removed)
				}
			}
		}
	case "potion_insect_boost":
		insects := c.family(pb.Family_INSECT)
		c.potionBuff(insects, int64(len(insects))*11, 0)
	case "potion_lure":
		for i := 0; i < 4; i++ {
			if c.add(pb.Family_INSECT, 0, 0, 0, &c.state.EffectRng) == "" && c.err == nil {
				c.potionBuff(c.family(pb.Family_INSECT), 10, 10)
			}
		}
	default:
		c.err = fmt.Errorf("INVALID_CARD: 药剂处理器不可执行: %s", handler)
	}
}
