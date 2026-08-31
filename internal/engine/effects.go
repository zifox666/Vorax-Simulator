package engine

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "vorax/internal/protocol"
)

type context struct {
	state          *pb.GameState
	rules          *Rules
	events         []*pb.GameEvent
	source         string
	parent         uint64
	steps, limit   int
	err            error
	removedMonster *pb.Monster
}

func (c *context) tick() bool {
	if c.err != nil {
		return false
	}
	c.steps++
	if c.steps > c.limit {
		c.err = fmt.Errorf("EFFECT_LIMIT: 连锁超过 %d 步，整次操作已回滚", c.limit)
		return false
	}
	return true
}

func (c *context) emit(kind, msg string, ids []string, a, q int64) {
	if !c.tick() {
		return
	}
	e := &pb.GameEvent{Sequence: uint64(len(c.events) + 1), ParentSequence: c.parent, Kind: kind, Source: c.source, TargetIds: append([]string{}, ids...), Message: msg, ActivityDelta: a, QuantityDelta: q}
	if card := c.rules.Card(c.source); card != nil {
		e.SourceName = card.Name
	}
	for _, slot := range c.state.Slots {
		snapshot := &pb.SlotSnapshot{Index: slot.Index}
		if slot.Monster != nil {
			snapshot.Monster = proto.Clone(slot.Monster).(*pb.Monster)
		}
		e.SlotsAfter = append(e.SlotsAfter, snapshot)
	}
	c.events = append(c.events, e)
	oldSource, oldParent := c.source, c.parent
	c.parent = e.Sequence
	for _, id := range append([]string{}, c.state.Tools...) {
		card := c.rules.Card(id)
		if card == nil || card.Trigger != kind {
			continue
		}
		c.source = id
		c.tool(card.Handler, e)
		if c.err != nil {
			break
		}
	}
	c.source, c.parent = oldSource, oldParent
}

func base(r pb.MonsterRarity) (int64, int64) {
	switch r {
	case pb.MonsterRarity_NORMAL:
		return 1, 36
	case pb.MonsterRarity_MAGIC:
		return 5, 24
	case pb.MonsterRarity_RARE:
		return 15, 12
	case pb.MonsterRarity_BOSS:
		return 300, 1
	}
	return 0, 0
}

// monsterRarityWeights 是随机生成怪物的稀有度权重：普通 45%、魔法 30%、稀有 20%、首领 5%。
// 卡牌未显式指定稀有度（r == 0）时，初始、添加、变异统一按此权重抽取。
var monsterRarityWeights = [4]int{45, 30, 20, 5}

// monsterRarityAt 把 0..99 的均匀随机数映射到稀有度桶，边界与权重一一对应，便于精确测试。
func monsterRarityAt(roll int) pb.MonsterRarity {
	for i, w := range monsterRarityWeights {
		if roll < w {
			return pb.MonsterRarity(i + 1)
		}
		roll -= w
	}
	return pb.MonsterRarity_BOSS
}

func monsterRarity(rng *uint64) pb.MonsterRarity {
	total := 0
	for _, w := range monsterRarityWeights {
		total += w
	}
	return monsterRarityAt(randomN(rng, total))
}

func (c *context) initialize(count int, family pb.Family, bones bool) {
	for i := 0; i < count; i++ {
		f := family
		if bones && i == 2 {
			f = pb.Family(2 + randomN(&c.state.InitRng, 3))
		}
		c.add(f, 0, 0, 0, &c.state.InitRng)
	}
	if c.err == nil {
		c.state.OpeningToolFamily = initialToolFamily(c.state)
	}
}

// initializeRares 是稀有怪堆的初始化：四个流派各一组稀有怪物，彼此种群不同。
func (c *context) initializeRares() {
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		c.add(family, pb.MonsterRarity_RARE, 0, 0, &c.state.InitRng)
	}
	if c.err == nil {
		c.state.OpeningToolFamily = initialToolFamily(c.state)
	}
}

func (c *context) add(f pb.Family, r pb.MonsterRarity, a, q int64, rng *uint64) string {
	return c.addMonster(f, r, a, q, rng, nil)
}

func (c *context) addMonster(f pb.Family, r pb.MonsterRarity, a, q int64, rng *uint64, definition *monsterDefinition) string {
	if !c.tick() {
		return ""
	}
	var free *pb.Slot
	for _, s := range c.state.Slots {
		if s.Monster == nil {
			free = s
			break
		}
	}
	if free == nil {
		c.emit("overflow", "培养槽已满，添加溢出", nil, 0, 0)
		return ""
	}
	if f == 0 {
		f = pb.Family(1 + randomN(rng, 4))
	}
	if r == 0 {
		r = monsterRarity(rng)
	}
	if definition == nil {
		definition = pickMonsterDefinition(f, r, rng)
	}
	if definition == nil {
		c.err = fmt.Errorf("INVALID_CONTENT: 怪物定义不存在")
		return ""
	}
	ba, bq := base(definition.rarity)
	aa, err := checkedAdd(ba, a)
	if err != nil {
		c.err = err
		return ""
	}
	qq, err := checkedAdd(bq, q)
	if err != nil {
		c.err = err
		return ""
	}
	id := fmt.Sprintf("monster-%d", c.state.NextMonsterId)
	c.state.NextMonsterId++
	free.Monster = &pb.Monster{Id: id, Activity: aa, Quantity: qq}
	definition.identify(free.Monster)
	c.emit("added", "添加怪物", []string{id}, aa, qq)
	return id
}

func (c *context) buff(id string, a, q int64) {
	if !c.tick() {
		return
	}
	m := getMonster(c.state, id)
	if m == nil {
		return
	}
	aa, err := checkedAdd(m.Activity, a)
	if err != nil {
		c.err = err
		return
	}
	qq, err := checkedAdd(m.Quantity, q)
	if err != nil {
		c.err = err
		return
	}
	m.Activity, m.Quantity = aa, qq
	c.emit("stats_changed", fmt.Sprintf("%s 活性 +%d，数量 +%d", id, a, q), []string{id}, a, q)
}

func (c *context) remove(id string) {
	if !c.tick() {
		return
	}
	for _, s := range c.state.Slots {
		if s.Monster != nil && s.Monster.Id == id {
			previous := c.removedMonster
			c.removedMonster = proto.Clone(s.Monster).(*pb.Monster)
			s.Monster = nil
			c.emit("removed", "移除怪物", []string{id}, 0, 0)
			c.removedMonster = previous
			return
		}
	}
}

func (c *context) transform(id string, f pb.Family, r pb.MonsterRarity, awakening bool, levels int32) {
	m := getMonster(c.state, id)
	if m == nil {
		return
	}
	kind := "mutated"
	if awakening {
		kind = "awakened"
		f = m.Family
		if r == 0 {
			if m.Rarity == pb.MonsterRarity_BOSS {
				return
			}
			r = m.Rarity + pb.MonsterRarity(levels)
			if r > pb.MonsterRarity_BOSS {
				r = pb.MonsterRarity_BOSS
			}
		}
	} else {
		if f == 0 {
			f = pb.Family(1 + randomN(&c.state.EffectRng, 4))
		}
		if r == 0 {
			r = monsterRarity(&c.state.EffectRng)
		}
	}
	c.transformTo(id, pickMonsterDefinition(f, r, &c.state.EffectRng), kind)
}

func (c *context) transformTo(id string, definition *monsterDefinition, kind string) {
	if !c.tick() {
		return
	}
	m := getMonster(c.state, id)
	if m == nil {
		return
	}
	if definition == nil {
		c.err = fmt.Errorf("INVALID_CONTENT: 怪物定义不存在")
		return
	}
	a, q := base(definition.rarity)
	aa, err := checkedAdd(m.Activity, a)
	if err != nil {
		c.err = err
		return
	}
	qq, err := checkedAdd(m.Quantity, q)
	if err != nil {
		c.err = err
		return
	}
	m.Activity, m.Quantity = aa, qq
	definition.identify(m)
	c.emit(kind, "保留当前属性并叠加目标基础属性", []string{id}, a, q)
}

func (c *context) fuse(ids []string, f pb.Family, r pb.MonsterRarity) {
	if !c.tick() {
		return
	}
	if len(ids) == 0 {
		return
	}
	var a, q int64
	maxR := pb.MonsterRarity_NORMAL
	dest := -1
	selected := map[string]bool{}
	for _, id := range ids {
		if selected[id] {
			c.err = fmt.Errorf("INVALID_TARGET: 融合目标重复")
			return
		}
		selected[id] = true
		m := getMonster(c.state, id)
		if m == nil {
			return
		}
		var err error
		a, err = checkedAdd(a, m.Activity)
		if err != nil {
			c.err = err
			return
		}
		q, err = checkedAdd(q, m.Quantity)
		if err != nil {
			c.err = err
			return
		}
		if m.Rarity > maxR {
			maxR = m.Rarity
		}
	}
	for i, s := range c.state.Slots {
		if s.Monster != nil && selected[s.Monster.Id] {
			if dest < 0 {
				dest = i
			}
			s.Monster = nil
		}
	}
	if f == 0 {
		f = pb.Family(1 + randomN(&c.state.EffectRng, 4))
	}
	if r == 0 {
		r = maxR + 1
		if r > pb.MonsterRarity_BOSS {
			r = pb.MonsterRarity_BOSS
		}
	}
	id := fmt.Sprintf("monster-%d", c.state.NextMonsterId)
	c.state.NextMonsterId++
	definition := pickMonsterDefinition(f, r, &c.state.EffectRng)
	if definition == nil {
		c.err = fmt.Errorf("INVALID_CONTENT: 怪物定义不存在")
		return
	}
	c.state.Slots[dest].Monster = &pb.Monster{Id: id, Activity: a, Quantity: q}
	definition.identify(c.state.Slots[dest].Monster)
	c.emit("fused", "融合完成；不触发普通移除或添加", append(append([]string{}, ids...), id), 0, 0)
}

func (c *context) devour(eaterID, preyID string) {
	if !c.tick() {
		return
	}
	if eaterID == preyID {
		return
	}
	eater, prey := getMonster(c.state, eaterID), getMonster(c.state, preyID)
	if eater == nil || prey == nil {
		return
	}
	a, err := checkedAdd(eater.Activity, prey.Activity)
	if err != nil {
		c.err = err
		return
	}
	q, err := checkedAdd(eater.Quantity, prey.Quantity)
	if err != nil {
		c.err = err
		return
	}
	eater.Activity, eater.Quantity = a, q
	for _, s := range c.state.Slots {
		if s.Monster != nil && s.Monster.Id == preyID {
			s.Monster = nil
		}
	}
	c.emit("devoured", "吞噬完成；不触发普通移除", []string{eaterID, preyID}, prey.Activity, prey.Quantity)
}

func (c *context) selected(selector string, ids []string) []string {
	if selector == "selected" {
		return append([]string{}, ids...)
	}
	out := []string{}
	for _, s := range c.state.Slots {
		m := s.Monster
		if m != nil && (selector == "all" || (selector == "awakeners" && m.Family == pb.Family_AWAKENER)) {
			out = append(out, m.Id)
		}
	}
	return out
}

func (c *context) play(card *pb.CardDefinition, ids []string) {
	for _, effect := range card.Effects {
		if !c.tick() {
			return
		}
		if effect.Op == "add" {
			for i := int32(0); i < effect.Count; i++ {
				c.add(effect.Family, effect.Rarity, effect.Activity, effect.Quantity, &c.state.EffectRng)
			}
			continue
		}
		for _, id := range c.selected(effect.Selector, ids) {
			switch effect.Op {
			case "buff":
				c.buff(id, effect.Activity, effect.Quantity)
			case "mutate":
				c.transform(id, effect.Family, effect.Rarity, false, 0)
			case "awaken":
				c.transform(id, 0, 0, true, effect.Count)
			case "remove_left":
				for i, s := range c.state.Slots {
					if s.Monster != nil && s.Monster.Id == id {
						for j := i - 1; j >= 0; j-- {
							if c.state.Slots[j].Monster != nil {
								c.remove(c.state.Slots[j].Monster.Id)
								break
							}
						}
						break
					}
				}
			}
		}
	}
	c.applyPotion(card.Handler, ids)
}

func (c *context) family(f pb.Family) []string {
	out := []string{}
	for _, s := range c.state.Slots {
		if s.Monster != nil && s.Monster.Family == f {
			out = append(out, s.Monster.Id)
		}
	}
	return out
}

func (c *context) extreme(high bool, product bool, exclude string) string {
	var best *pb.Monster
	var value int64
	for _, slot := range c.state.Slots {
		m := slot.Monster
		if m == nil || m.Id == exclude {
			continue
		}
		v := m.Activity
		if product {
			var err error
			v, err = contribution(m)
			if err != nil {
				c.err = err
				return ""
			}
		}
		if best == nil || (high && v > value) || (!high && v < value) {
			best = m
			value = v
		}
	}
	if best == nil {
		return ""
	}
	return best.Id
}

func (c *context) tool(handler string, e *pb.GameEvent) {
	if c.tick() {
		c.applyTool(handler, e)
	}
}

// GameplayCopy removes transport identity and revisions from replay comparison.
func GameplayCopy(s *pb.GameState) *pb.GameState {
	out := proto.Clone(s).(*pb.GameState)
	out.RunId = ""
	out.UserId = ""
	out.Revision = 0
	return out
}
