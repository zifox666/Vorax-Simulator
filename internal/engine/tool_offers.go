package engine

import pb "vorax/internal/protocol"

func coreToolFamily(id string) pb.Family {
	switch id {
	case "claw", "metatarsal", "sinew":
		return pb.Family_BONE
	case "marrow", "growth", "liver":
		return pb.Family_FIEND
	case "pituitary", "frontal_lobe":
		return pb.Family_AWAKENER
	case "pupa", "cluster_eggs", "hatching_egg":
		return pb.Family_INSECT
	}
	return pb.Family_FAMILY_UNSPECIFIED
}

func isOpeningToolOffer(s *pb.GameState, threshold int64) bool {
	return s.BaseCursor == 0 && threshold == 0
}

func initialToolFamily(s *pb.GameState) pb.Family {
	counts := [5]int{}
	for _, slot := range s.Slots {
		if m := slot.Monster; m != nil && m.Family >= pb.Family_BONE && m.Family <= pb.Family_INSECT {
			counts[m.Family]++
		}
	}
	most := 0
	leaders := []pb.Family{}
	for family := pb.Family_BONE; family <= pb.Family_INSECT; family++ {
		if counts[family] > most {
			most = counts[family]
			leaders = []pb.Family{family}
		} else if most > 0 && counts[family] == most {
			leaders = append(leaders, family)
		}
	}
	if len(leaders) == 0 {
		return pb.Family_FAMILY_UNSPECIFIED
	}
	if len(leaders) == 1 {
		return leaders[0]
	}
	for _, family := range leaders {
		if family == pb.Family_AWAKENER {
			return pb.Family_AWAKENER
		}
	}
	return leaders[randomN(&s.InitRng, len(leaders))]
}

func openingToolWeights(pool []*pb.CardDefinition, family pb.Family) ([]int, int) {
	matching := 0
	for _, card := range pool {
		if card.CoreFamily == family {
			matching++
		}
	}
	other := len(pool) - matching
	weights := make([]int, len(pool))
	total := 0
	for i, card := range pool {
		weight := 1
		if matching > 0 && other > 0 {
			if card.CoreFamily == family {
				weight = 11 * other
			} else {
				weight = 9 * matching
			}
		}
		weights[i] = weight
		total += weight
	}
	return weights, total
}

func toolIndexAt(roll int, weights []int) int {
	for i, weight := range weights {
		if roll < weight {
			return i
		}
		roll -= weight
	}
	return -1
}
