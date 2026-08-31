package engine

import pb "vorax/internal/protocol"

type monsterDefinition struct {
	id     string
	name   string
	family pb.Family
	rarity pb.MonsterRarity
}

var monsterDefinitions = []monsterDefinition{
	{"bone_soldier", "士兵", pb.Family_BONE, pb.MonsterRarity_NORMAL},
	{"bone_archer", "弓兵", pb.Family_BONE, pb.MonsterRarity_NORMAL},
	{"bone_light_crossbowman", "轻弩兵", pb.Family_BONE, pb.MonsterRarity_MAGIC},
	{"bone_rust_halberdier", "锈戟兵", pb.Family_BONE, pb.MonsterRarity_MAGIC},
	{"bone_shield_guard", "刚盾卫兵", pb.Family_BONE, pb.MonsterRarity_RARE},
	{"bone_patrol_guard", "巡察卫兵", pb.Family_BONE, pb.MonsterRarity_RARE},
	{"bone_warden", "骸骨典狱官", pb.Family_BONE, pb.MonsterRarity_BOSS},
	{"fiend_beast", "异兽", pb.Family_FIEND, pb.MonsterRarity_NORMAL},
	{"fiend_rotten_sac_beast", "腐囊异兽", pb.Family_FIEND, pb.MonsterRarity_NORMAL},
	{"fiend_scorpion_beast", "蝎兽", pb.Family_FIEND, pb.MonsterRarity_NORMAL},
	{"fiend_spiny_lizard", "刺蜥兽", pb.Family_FIEND, pb.MonsterRarity_MAGIC},
	{"fiend_split_jaw_beast", "裂颚兽", pb.Family_FIEND, pb.MonsterRarity_MAGIC},
	{"fiend_evil_eye_winged_beast", "邪眼翼兽", pb.Family_FIEND, pb.MonsterRarity_RARE},
	{"fiend_red_plague_cerberus", "红瘟三头犬", pb.Family_FIEND, pb.MonsterRarity_BOSS},
	{"awakener_scholar", "学者", pb.Family_AWAKENER, pb.MonsterRarity_NORMAL},
	{"awakener_sage", "智者", pb.Family_AWAKENER, pb.MonsterRarity_NORMAL},
	{"awakener_fallen", "堕落者", pb.Family_AWAKENER, pb.MonsterRarity_MAGIC},
	{"awakener_pilgrim", "朝圣者", pb.Family_AWAKENER, pb.MonsterRarity_MAGIC},
	{"awakener_heretic_scholar", "异端学者", pb.Family_AWAKENER, pb.MonsterRarity_RARE},
	{"awakener_forbidden_tome_scholar", "禁典学者", pb.Family_AWAKENER, pb.MonsterRarity_RARE},
	{"awakener_ascetic_pontiff", "苦行大教宗", pb.Family_AWAKENER, pb.MonsterRarity_BOSS},
	{"awakener_ascended_saint", "飞升之圣徒", pb.Family_AWAKENER, pb.MonsterRarity_BOSS},
	{"insect_spider", "恶蛛", pb.Family_INSECT, pb.MonsterRarity_NORMAL},
	{"insect_gray_moth", "灰蛾", pb.Family_INSECT, pb.MonsterRarity_NORMAL},
	{"insect_rot_fly", "腐汁蝇", pb.Family_INSECT, pb.MonsterRarity_MAGIC},
	{"insect_red_mantis", "赤螳螂", pb.Family_INSECT, pb.MonsterRarity_MAGIC},
	{"insect_parasitic_butterfly", "寄生蝴蝶", pb.Family_INSECT, pb.MonsterRarity_RARE},
	{"insect_red_spotted_beetle", "红斑金龟", pb.Family_INSECT, pb.MonsterRarity_RARE},
	{"insect_gluttonous_worm", "暴食巨蠕虫", pb.Family_INSECT, pb.MonsterRarity_BOSS},
	{"insect_hollow_cocoon", "空心茧", pb.Family_INSECT, pb.MonsterRarity_BOSS},
}

func findMonsterDefinition(id string) *monsterDefinition {
	for i := range monsterDefinitions {
		if monsterDefinitions[i].id == id {
			return &monsterDefinitions[i]
		}
	}
	return nil
}

func pickMonsterDefinition(f pb.Family, r pb.MonsterRarity, rng *uint64) *monsterDefinition {
	pool := []*monsterDefinition{}
	for i := range monsterDefinitions {
		d := &monsterDefinitions[i]
		if d.family == f && d.rarity == r {
			pool = append(pool, d)
		}
	}
	if len(pool) == 0 {
		return nil
	}
	return pool[randomN(rng, len(pool))]
}

func (d *monsterDefinition) identify(m *pb.Monster) {
	m.DefinitionId, m.Name, m.Family, m.Rarity = d.id, d.name, d.family, d.rarity
}

func definitionOf(m *pb.Monster) *monsterDefinition {
	return &monsterDefinition{id: m.DefinitionId, name: m.Name, family: m.Family, rarity: m.Rarity}
}
