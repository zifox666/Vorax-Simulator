package engine

import (
	"fmt"

	pb "vorax/internal/protocol"
)

func toolCards() []*pb.CardDefinition {
	entries := []struct {
		id, name, description, trigger string
		enabled                        bool
	}{
		{"claw", "栾缩指爪", "移除非骨卫兵的怪物时，随机一组怪物+150数量。", "removed", true},
		{"cortex", "蜕生脑皮层", "回合结束时，随机2组魔法怪物融合为稀有觉醒者。", "turn_end", true},
		{"goat_suture", "黑山羊肠缝线", "回合结束时，每拥有3组相同稀有度的怪物将其融合为1组更高稀有度的怪物。", "turn_end", true},
		{"marrow", "蠕动脊髓", "每次添加怪物时，75%概率将其变为异魔，并使其+80活性。", "added", true},
		{"saw", "生铁骨锯", "回合结束时，数量和活性都大于150的怪物，+15活性和+15数量。", "turn_end", true},
		{"scraper", "脏污刮骨刀", "回合结束时，所有数量大于275的怪物+20活性。", "turn_end", true},
		{"eye", "眼睑扩张器", "移除怪物时，使活性最高的一组怪物+20活性和+20数量。", "removed", true},
		{"metatarsal", "粘连跖骨", "拥有至少2组骨卫兵时，每次移除怪物，使随机1组怪物+180数量。", "removed", true},
		{"sinew", "兽筋绞肉索", "回合结束时，如果拥有至少2组骨卫兵，移除活性最低的非骨卫兵怪物，随机1组骨卫兵获得其活性。", "turn_end", true},
		{"tanned_restraint", "鞣制皮革拘束带", "同时拥有普通、魔法、劲敌怪物时，每回合所有怪物+20活性。", "turn_end", true},
		{"pituitary", "肿大脑垂体", "拥有稀有或首领觉醒者时，每回合使随机1组怪物+80活性。", "turn_end", true},
		{"fang", "犬牙锉刀", "回合结束时，所有活性大于255的怪物+30数量。", "turn_end", true},
		{"frontal_lobe", "增生前额叶", "至少拥有2组觉醒者时，每回合使随机1组稀有或首领怪物+80活性。", "turn_end", true},
		{"rawhide_restraint", "生皮革拘束带", "拥有至少3组魔法怪物时，每回合使活性最高的1组怪物+100数量。", "turn_end", true},
		{"growth", "孽生肉芽", "怪物变异为异魔时，所有怪物+35活性。", "mutated", true},
		{"nettle", "荨麻绳扣", "拥有至少3组普通怪物时，每回合使所有怪物+30数量。", "turn_end", true},
		{"liver", "斑斓肝脏", "拥有至少2组异魔时，每次变异怪物，使所有怪物+20活性。", "mutated", true},
		{"pupa", "人蛹标本", "回合结束时，根据当前拥有的蛊虫数X，随机X组怪物+8数量，重复生效X次。", "turn_end", true},
		{"statue", "疫区圣母像", "每2回合，每个稀有度内随机1组怪物觉醒为高1阶稀有度的怪物。", "turn_end", true},
		{"cluster_eggs", "簇生虫卵", "拥有至少3组蛊虫时，每回合添加1组蛊虫，并使其+100数量。", "turn_end", true},
		{"nail", "二寸颅骨钉", "添加怪物时，若超过培养皿数量上限，使活性最低的1组怪物+25活性和+25数量。", "overflow", true},
		{"mouth", "二度降生者之嘴", "回合结束时，如果有2组怪物，添加1组怪物；如果有6组怪物，活性最高的怪物吞噬总活性最低的怪物。", "turn_end", true},
		{"hatching_egg", "孵化卵", "拥有至少2组蛊虫时，每次添加怪物时，使所有怪物+45数量。", "added", true},
		{"brooding_butterfly", "抱卵蝶", "每3回合，随机2组蛊虫融合为1组随机蛊虫。", "turn_end", true},
	}
	cards := make([]*pb.CardDefinition, 0, len(entries))
	for _, entry := range entries {
		cards = append(cards, &pb.CardDefinition{
			Id: entry.id, Name: entry.name, Description: entry.description,
			Kind: pb.CardKind_TOOL, Handler: entry.id, Trigger: entry.trigger, Enabled: entry.enabled, CoreFamily: coreToolFamily(entry.id),
		})
	}
	return cards
}

func (c *context) toolFilter(match func(*pb.Monster) bool) []string {
	ids := []string{}
	for _, slot := range c.state.Slots {
		if slot.Monster != nil && match(slot.Monster) {
			ids = append(ids, slot.Monster.Id)
		}
	}
	return ids
}

func (c *context) toolRarity(rarity pb.MonsterRarity) []string {
	return c.toolFilter(func(m *pb.Monster) bool { return m.Rarity == rarity })
}

func (c *context) toolSample(ids []string, count int) []string {
	pool := append([]string{}, ids...)
	if count > len(pool) {
		count = len(pool)
	}
	selected := make([]string, 0, count)
	for len(selected) < count {
		i := randomN(&c.state.EffectRng, len(pool))
		selected = append(selected, pool[i])
		pool = append(pool[:i], pool[i+1:]...)
	}
	return selected
}

func (c *context) toolBuff(ids []string, activity, quantity int64) {
	for _, id := range ids {
		c.buff(id, activity, quantity)
		if c.err != nil {
			return
		}
	}
}

func (c *context) toolEventMonster(event *pb.GameEvent) *pb.Monster {
	if event == nil || len(event.TargetIds) == 0 {
		return nil
	}
	return getMonster(c.state, event.TargetIds[0])
}

func (c *context) applyTool(handler string, event *pb.GameEvent) {
	switch handler {
	case "claw":
		if c.removedMonster != nil && c.removedMonster.Family != pb.Family_BONE {
			c.toolBuff(c.toolSample(monsterIDs(c.state), 1), 0, 150)
		}
	case "cortex":
		ids := c.toolRarity(pb.MonsterRarity_MAGIC)
		if len(ids) >= 2 {
			c.fuse(c.toolSample(ids, 2), pb.Family_AWAKENER, pb.MonsterRarity_RARE)
		}
	case "goat_suture":
		groups := make([][]string, 0, 3)
		for rarity := pb.MonsterRarity_NORMAL; rarity < pb.MonsterRarity_BOSS; rarity++ {
			groups = append(groups, c.toolRarity(rarity))
		}
		for index, group := range groups {
			for len(group) >= 3 {
				c.fuse(group[:3], 0, pb.MonsterRarity(index+2))
				if c.err != nil {
					return
				}
				group = group[3:]
			}
		}
	case "marrow":
		m := c.toolEventMonster(event)
		if m != nil && randomN(&c.state.EffectRng, 100) < 75 {
			c.transform(m.Id, pb.Family_FIEND, 0, false, 0)
			c.buff(m.Id, 80, 0)
		}
	case "saw":
		c.toolBuff(c.toolFilter(func(m *pb.Monster) bool { return m.Activity > 150 && m.Quantity > 150 }), 15, 15)
	case "scraper":
		c.toolBuff(c.toolFilter(func(m *pb.Monster) bool { return m.Quantity > 275 }), 20, 0)
	case "eye":
		c.buff(c.extreme(true, false, ""), 20, 20)
	case "metatarsal":
		if len(c.family(pb.Family_BONE)) >= 2 {
			c.toolBuff(c.toolSample(monsterIDs(c.state), 1), 0, 180)
		}
	case "sinew":
		if len(c.family(pb.Family_BONE)) < 2 {
			return
		}
		var lowest *pb.Monster
		for _, slot := range c.state.Slots {
			m := slot.Monster
			if m != nil && m.Family != pb.Family_BONE && (lowest == nil || m.Activity < lowest.Activity) {
				lowest = m
			}
		}
		if lowest != nil {
			activity := lowest.Activity
			c.remove(lowest.Id)
			if c.err == nil {
				c.toolBuff(c.toolSample(c.family(pb.Family_BONE), 1), activity, 0)
			}
		}
	case "tanned_restraint":
		if len(c.toolRarity(pb.MonsterRarity_NORMAL)) > 0 && len(c.toolRarity(pb.MonsterRarity_MAGIC)) > 0 && len(c.toolRarity(pb.MonsterRarity_BOSS)) > 0 {
			c.toolBuff(monsterIDs(c.state), 20, 0)
		}
	case "pituitary":
		eligible := c.toolFilter(func(m *pb.Monster) bool { return m.Family == pb.Family_AWAKENER && m.Rarity >= pb.MonsterRarity_RARE })
		if len(eligible) > 0 {
			c.toolBuff(c.toolSample(monsterIDs(c.state), 1), 80, 0)
		}
	case "fang":
		c.toolBuff(c.toolFilter(func(m *pb.Monster) bool { return m.Activity > 255 }), 0, 30)
	case "frontal_lobe":
		if len(c.family(pb.Family_AWAKENER)) >= 2 {
			eligible := c.toolFilter(func(m *pb.Monster) bool { return m.Rarity >= pb.MonsterRarity_RARE })
			c.toolBuff(c.toolSample(eligible, 1), 80, 0)
		}
	case "rawhide_restraint":
		if len(c.toolRarity(pb.MonsterRarity_MAGIC)) >= 3 {
			c.buff(c.extreme(true, false, ""), 0, 100)
		}
	case "growth":
		if m := c.toolEventMonster(event); m != nil && m.Family == pb.Family_FIEND {
			c.toolBuff(monsterIDs(c.state), 35, 0)
		}
	case "nettle":
		if len(c.toolRarity(pb.MonsterRarity_NORMAL)) >= 3 {
			c.toolBuff(monsterIDs(c.state), 0, 30)
		}
	case "liver":
		if len(c.family(pb.Family_FIEND)) >= 2 {
			c.toolBuff(monsterIDs(c.state), 20, 0)
		}
	case "pupa":
		count := len(c.family(pb.Family_INSECT))
		for i := 0; i < count && c.err == nil; i++ {
			c.toolBuff(c.toolSample(monsterIDs(c.state), count), 0, 8)
		}
	case "statue":
		if c.state.CompletedTurns <= 0 || c.state.CompletedTurns%2 != 0 {
			return
		}
		groups := make([][]string, 0, 3)
		for rarity := pb.MonsterRarity_NORMAL; rarity < pb.MonsterRarity_BOSS; rarity++ {
			groups = append(groups, c.toolRarity(rarity))
		}
		for _, group := range groups {
			for _, id := range c.toolSample(group, 1) {
				c.transform(id, 0, 0, true, 1)
				if c.err != nil {
					return
				}
			}
		}
	case "cluster_eggs":
		if len(c.family(pb.Family_INSECT)) >= 3 {
			c.add(pb.Family_INSECT, 0, 0, 100, &c.state.EffectRng)
		}
	case "nail":
		c.buff(c.extreme(false, false, ""), 25, 25)
	case "mouth":
		switch len(monsterIDs(c.state)) {
		case 2:
			c.add(0, 0, 0, 0, &c.state.EffectRng)
		case 6:
			eater := c.extreme(true, false, "")
			prey := c.extreme(false, true, eater)
			if c.err == nil {
				c.devour(eater, prey)
			}
		}
	case "hatching_egg":
		if len(c.family(pb.Family_INSECT)) >= 2 {
			c.toolBuff(monsterIDs(c.state), 0, 45)
		}
	case "brooding_butterfly":
		if c.state.CompletedTurns > 0 && c.state.CompletedTurns%3 == 0 {
			ids := c.family(pb.Family_INSECT)
			if len(ids) >= 2 {
				selected := c.toolSample(ids, 2)
				rarity := monsterRarity(&c.state.EffectRng)
				c.fuse(selected, pb.Family_INSECT, rarity)
			}
		}
	default:
		c.err = fmt.Errorf("INVALID_CARD: 手术用具处理器无效")
	}
}
