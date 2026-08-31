package engine

import pb "vorax/internal/protocol"

type MonsterEntry struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Family int32  `json:"family"`
	Rarity int32  `json:"rarity"`
}

func MonsterCatalog() []MonsterEntry {
	result := make([]MonsterEntry, 0, len(monsterDefinitions))
	for _, d := range monsterDefinitions {
		result = append(result, MonsterEntry{d.id, d.name, int32(d.family), int32(d.rarity)})
	}
	return result
}

// RecalculateVisibleRewards refreshes a reconstructed state's score and display
// rewards, without inferring historical unlocks from a transient score.
func RecalculateVisibleRewards(s *pb.GameState) error { return updateRewards(s, false) }
