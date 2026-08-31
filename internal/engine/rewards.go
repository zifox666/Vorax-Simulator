package engine

import (
	"fmt"
	"math"

	pb "vorax/internal/protocol"
)

var Thresholds = []int64{2000, 8000, 18000, 28000, 38000, 57000, 76000, 114000, 152000, 190000, 266000, 342000, 418000, 512000, 607000, 721000, 854000, 1120000}

func checkedAdd(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > math.MaxInt64-b {
		return 0, fmt.Errorf("NUMERIC_OVERFLOW: 属性超出安全范围")
	}
	return a + b, nil
}
func contribution(m *pb.Monster) (int64, error) {
	if m == nil {
		return 0, nil
	}
	if m.Activity < 0 || m.Quantity < 0 || (m.Quantity != 0 && m.Activity > math.MaxInt64/m.Quantity) {
		return 0, fmt.Errorf("NUMERIC_OVERFLOW: 分数超出安全范围")
	}
	return m.Activity * m.Quantity, nil
}

func updateRewards(s *pb.GameState, unlock bool) error {
	var score int64
	for _, slot := range s.Slots {
		x, err := contribution(slot.Monster)
		if err != nil {
			return err
		}
		slot.Contribution = x
		score, err = checkedAdd(score, x)
		if err != nil {
			return err
		}
	}
	s.Score = score
	if score > s.PeakScore {
		s.PeakScore = score
	}
	r := s.Rewards
	r.Jars = make([]pb.JarColor, 6)
	r.DropBonusPercent = 0
	r.NextThreshold = 0
	r.NextRewardLabel = "全部奖励已解锁"
	if score >= 2000 {
		for i := 0; i < 3; i++ {
			r.Jars[i] = pb.JarColor_JAR_WHITE
		}
	}
	if score >= 18000 {
		for i := 0; i < 6; i++ {
			r.Jars[i] = pb.JarColor_JAR_WHITE
		}
	}
	for i, t := range Thresholds[4:10] {
		if score >= t {
			r.Jars[i] = pb.JarColor_PURPLE
		}
	}
	for i, t := range Thresholds[10:16] {
		if score >= t {
			r.Jars[i] = pb.JarColor_JAR_RED
		}
	}
	if score >= 854000 {
		r.DropBonusPercent = 15
	}
	if score >= 1120000 {
		r.Jars[0] = pb.JarColor_RAINBOW
	}
	if unlock {
		for _, claim := range r.ToolClaims {
			if claim.Status == pb.ClaimStatus_LOCKED && score >= claim.Threshold {
				claim.Status = pb.ClaimStatus_PENDING
			}
		}
	}
	for _, t := range Thresholds {
		if t <= score {
			continue
		}
		already := false
		for _, claim := range r.ToolClaims {
			if claim.Threshold == t && claim.Status != pb.ClaimStatus_LOCKED {
				already = true
			}
		}
		if !already {
			r.NextThreshold = t
			switch {
			case t == 2000 || t == 18000:
				r.NextRewardLabel = "+3 个白色奖励罐"
			case t == 8000 || t == 28000:
				r.NextRewardLabel = "手术用具选择"
			case t >= 38000 && t <= 190000:
				r.NextRewardLabel = "1 个奖励罐升级为紫色"
			case t >= 266000 && t <= 721000:
				r.NextRewardLabel = "1 个奖励罐升级为红色"
			case t == 854000:
				r.NextRewardLabel = "掉落数量 +15%"
			case t == 1120000:
				r.NextRewardLabel = "1 个奖励罐升级为彩色"
			}
			break
		}
	}
	return nil
}
