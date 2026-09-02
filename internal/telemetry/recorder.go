// Package telemetry stores privacy-preserving gameplay transitions for model
// training. It never persists transport identity, IP addresses, request IDs,
// state tokens, raw player IDs, raw run IDs, or hidden RNG cursor values.
package telemetry

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"vorax/internal/ai"
	pb "vorax/internal/protocol"
)

const writeTimeout = 3 * time.Second

// TrainingEpisode contains only a keyed pseudonym plus reproducibility data.
// Seed is intentionally retained verbatim for deterministic replay; the UI
// tells players not to put personal information in it.
type TrainingEpisode struct {
	ID                    string    `gorm:"primaryKey;size:64"`
	PlayerPseudonym       string    `gorm:"size:64;not null;index"`
	Seed                  string    `gorm:"size:256;not null"`
	RulesVersion          string    `gorm:"size:80;not null;index"`
	ContentVersion        string    `gorm:"size:80;not null;index"`
	RNGVersion            string    `gorm:"size:80;not null"`
	PetRefreshes          int32     `gorm:"not null"`
	FirstObservedRevision uint64    `gorm:"not null"`
	FirstGameplayHash     string    `gorm:"size:64;not null"`
	InitialObservation    string    `gorm:"type:jsonb;not null"`
	StartedAt             time.Time `gorm:"not null"`
	LastSeenAt            time.Time `gorm:"not null;index"`
	TransitionCount       int64     `gorm:"not null;default:0"`
}

func (TrainingEpisode) TableName() string { return "training_episodes" }

// TrainingTransition is one directly usable (observation, action,
// next_observation) sample. Multiple branches from the same signed checkpoint
// are preserved as independent transitions.
type TrainingTransition struct {
	ID                  string    `gorm:"primaryKey;size:64"`
	EpisodeID           string    `gorm:"size:64;not null;index"`
	BeforeGameplayHash  string    `gorm:"size:64;not null;index"`
	AfterGameplayHash   string    `gorm:"size:64;not null"`
	RevisionBefore      uint64    `gorm:"not null"`
	RevisionAfter       uint64    `gorm:"not null"`
	ActionType          string    `gorm:"size:32;not null;index"`
	SelectedCardID      string    `gorm:"size:128"`
	SelectedTargetSlots string    `gorm:"type:jsonb;not null"`
	ObservationBefore   string    `gorm:"type:jsonb;not null"`
	ObservationAfter    string    `gorm:"type:jsonb;not null"`
	Events              string    `gorm:"type:jsonb;not null"`
	ScoreAfter          int64     `gorm:"not null"`
	Terminal            bool      `gorm:"not null;index"`
	RecordedAt          time.Time `gorm:"not null;index"`
}

func (TrainingTransition) TableName() string { return "training_transitions" }

type PostgresRecorder struct {
	db  *gorm.DB
	key []byte
}

func OpenPostgres(ctx context.Context, dsn string, pseudonymKey []byte) (*PostgresRecorder, error) {
	if dsn == "" {
		return nil, nil
	}
	if len(pseudonymKey) < 32 {
		return nil, fmt.Errorf("训练数据脱敏密钥至少需要 32 字节")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("连接训练数据库失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(8)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := db.WithContext(ctx).AutoMigrate(&TrainingEpisode{}, &TrainingTransition{}); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("迁移训练数据表失败: %w", err)
	}
	return &PostgresRecorder{db: db, key: append([]byte(nil), pseudonymKey...)}, nil
}

func (r *PostgresRecorder) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	db, err := r.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

func (r *PostgresRecorder) pseudonym(label, value string) string {
	m := hmac.New(sha256.New, r.key)
	_, _ = m.Write([]byte(label + "\x00" + value))
	return hex.EncodeToString(m.Sum(nil))
}

func marshalJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (r *PostgresRecorder) episode(state *pb.GameState, gameplayHash string, now time.Time) (*TrainingEpisode, error) {
	observation, err := marshalJSON(ai.FromGameState(state))
	if err != nil {
		return nil, err
	}
	return &TrainingEpisode{
		ID: r.pseudonym("episode", state.RunId), PlayerPseudonym: r.pseudonym("player", state.UserId),
		Seed: state.Seed, RulesVersion: state.RulesVersion, ContentVersion: state.ContentVersion,
		RNGVersion: state.RngVersion, PetRefreshes: state.InitialPetRefreshes,
		FirstObservedRevision: state.Revision, FirstGameplayHash: gameplayHash, InitialObservation: observation,
		StartedAt: now.UTC(), LastSeenAt: now.UTC(),
	}, nil
}

func targetSlots(state *pb.GameState, targetIDs []string) ([]int32, error) {
	byID := make(map[string]int32, len(state.Slots))
	for _, slot := range state.Slots {
		if slot.Monster != nil {
			byID[slot.Monster.Id] = slot.Index
		}
	}
	result := make([]int32, 0, len(targetIDs))
	for _, id := range targetIDs {
		index, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("决策目标 %q 不在决策前观察中", id)
		}
		result = append(result, index)
	}
	return result, nil
}

func (r *PostgresRecorder) transition(before, after *pb.GameState, command *pb.Command, events []*pb.GameEvent, beforeHash, afterHash string, now time.Time) (*TrainingTransition, error) {
	if command == nil {
		return nil, fmt.Errorf("缺少决策")
	}
	slots, err := targetSlots(before, command.TargetIds)
	if err != nil {
		return nil, err
	}
	slotsJSON, err := marshalJSON(slots)
	if err != nil {
		return nil, err
	}
	beforeJSON, err := marshalJSON(ai.FromGameState(before))
	if err != nil {
		return nil, err
	}
	afterJSON, err := marshalJSON(ai.FromGameState(after))
	if err != nil {
		return nil, err
	}
	eventsJSON, err := marshalJSON(events)
	if err != nil {
		return nil, err
	}
	episodeID := r.pseudonym("episode", before.RunId)
	fingerprint := beforeHash + "\x00" + command.Type + "\x00" + command.CardId + "\x00" + slotsJSON + "\x00" + afterHash
	return &TrainingTransition{
		ID: r.pseudonym("transition", episodeID+"\x00"+fingerprint), EpisodeID: episodeID,
		BeforeGameplayHash: beforeHash, AfterGameplayHash: afterHash,
		RevisionBefore: before.Revision, RevisionAfter: after.Revision,
		ActionType: command.Type, SelectedCardID: command.CardId, SelectedTargetSlots: slotsJSON,
		ObservationBefore: beforeJSON, ObservationAfter: afterJSON, Events: eventsJSON,
		ScoreAfter: after.Score, Terminal: after.Phase == pb.Phase_FINISHED, RecordedAt: now.UTC(),
	}, nil
}

func (r *PostgresRecorder) RecordCreated(state *pb.GameState, gameplayHash string) error {
	now := time.Now()
	episode, err := r.episode(state, gameplayHash, now)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(episode).Error
}

func (r *PostgresRecorder) RecordDecision(before, after *pb.GameState, command *pb.Command, events []*pb.GameEvent, beforeHash, afterHash string) error {
	now := time.Now()
	episode, err := r.episode(before, beforeHash, now)
	if err != nil {
		return err
	}
	transition, err := r.transition(before, after, command, events, beforeHash, afterHash, now)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(episode).Error; err != nil {
			return err
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(transition)
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		return tx.Model(&TrainingEpisode{}).Where("id = ?", episode.ID).Updates(map[string]any{
			"last_seen_at": now.UTC(), "transition_count": gorm.Expr("transition_count + 1"),
		}).Error
	})
}
