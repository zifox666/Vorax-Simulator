package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
	"vorax/internal/engine"
)

type ContentBundle struct {
	RulesVersion   string `gorm:"primaryKey;size:80"`
	ContentVersion string `gorm:"primaryKey;size:80"`
	Checksum       string `gorm:"size:64;not null"`
	Payload        string `gorm:"type:jsonb;not null"`
}

// PostgreSQL stores immutable content only; there is no player/session table.
func LoadContent(ctx context.Context, dsn string) (*engine.Rules, error) {
	embedded := engine.DemoRules()
	if err := embedded.Validate(); err != nil {
		return nil, err
	}
	if dsn == "" {
		return embedded, nil
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("连接内容数据库失败")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()
	db = db.WithContext(ctx)
	if err := db.AutoMigrate(&ContentBundle{}); err != nil {
		return nil, fmt.Errorf("迁移内容数据库失败: %w", err)
	}
	b, err := json.Marshal(embedded)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	row := ContentBundle{RulesVersion: embedded.Version, ContentVersion: embedded.ContentVersion, Checksum: hex.EncodeToString(sum[:]), Payload: string(b)}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	var saved ContentBundle
	if err := db.First(&saved, "rules_version = ? AND content_version = ?", row.RulesVersion, row.ContentVersion).Error; err != nil {
		return nil, err
	}
	var result engine.Rules
	if err := json.Unmarshal([]byte(saved.Payload), &result); err != nil {
		return nil, err
	}
	canonical, err := json.Marshal(&result)
	if err != nil {
		return nil, err
	}
	actual := sha256.Sum256(canonical)
	if hex.EncodeToString(actual[:]) != row.Checksum || saved.Checksum != row.Checksum {
		return nil, fmt.Errorf("内容版本不可变：数据库内容与该版本的构建产物不一致")
	}
	return &result, result.Validate()
}
