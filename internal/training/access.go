package training

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	pb "vorax/internal/protocol"
)

const keyPrefix = "vxtrain"

type KeyRecord struct {
	ID                    string     `json:"id" gorm:"primaryKey;size:32"`
	Name                  string     `json:"name" gorm:"size:128;not null"`
	Prefix                string     `json:"prefix" gorm:"size:80;not null"`
	SecretHash            string     `json:"secretHash" gorm:"size:64;not null"`
	BucketCapacity        int64      `json:"bucketCapacity" gorm:"not null"`
	RefillTokensPerSecond float64    `json:"refillTokensPerSecond" gorm:"not null"`
	CreatedAt             time.Time  `json:"createdAt" gorm:"not null"`
	ExpiresAt             *time.Time `json:"expiresAt,omitempty"`
	RevokedAt             *time.Time `json:"revokedAt,omitempty"`
}

func (KeyRecord) TableName() string { return "training_api_keys" }

type KeyStore interface {
	List(context.Context) ([]KeyRecord, error)
	Get(context.Context, string) (*KeyRecord, error)
	Create(context.Context, *KeyRecord) error
	Update(context.Context, *KeyRecord) error
	Close() error
}

type localKeyStore struct {
	mu      sync.RWMutex
	path    string
	records map[string]KeyRecord
}

func OpenLocalKeyStore(path string) (KeyStore, error) {
	s := &localKeyStore{path: path, records: map[string]KeyRecord{}}
	b, err := os.ReadFile(path)
	if err == nil {
		var records []KeyRecord
		if err := json.Unmarshal(b, &records); err != nil {
			return nil, fmt.Errorf("训练 API Key 文件损坏: %w", err)
		}
		for _, record := range records {
			s.records[record.ID] = record
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *localKeyStore) List(context.Context) ([]KeyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]KeyRecord, 0, len(s.records))
	for _, record := range s.records {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (s *localKeyStore) Get(_ context.Context, id string) (*KeyRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	record, ok := s.records[id]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	copy := record
	return &copy, nil
}

func (s *localKeyStore) Create(_ context.Context, record *KeyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.records[record.ID]; exists {
		return fmt.Errorf("训练 API Key ID 冲突")
	}
	s.records[record.ID] = *record
	if err := s.persistLocked(); err != nil {
		delete(s.records, record.ID)
		return err
	}
	return nil
}

func (s *localKeyStore) Update(_ context.Context, record *KeyRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	before, exists := s.records[record.ID]
	if !exists {
		return gorm.ErrRecordNotFound
	}
	s.records[record.ID] = *record
	if err := s.persistLocked(); err != nil {
		s.records[record.ID] = before
		return err
	}
	return nil
}

func (s *localKeyStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	records := make([]KeyRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].CreatedAt.Before(records[j].CreatedAt) })
	b, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(s.path)
		if retry := os.Rename(tmp, s.path); retry != nil {
			_ = os.Remove(tmp)
			return retry
		}
	}
	return os.Chmod(s.path, 0600)
}

func (s *localKeyStore) Close() error { return nil }

type postgresKeyStore struct {
	db    *gorm.DB
	sqlDB interface{ Close() error }
}

func OpenPostgresKeyStore(dsn string) (KeyStore, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		return nil, fmt.Errorf("连接训练密钥数据库失败: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err := db.AutoMigrate(&KeyRecord{}); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("迁移训练密钥数据库失败: %w", err)
	}
	return &postgresKeyStore{db: db, sqlDB: sqlDB}, nil
}

func (s *postgresKeyStore) List(ctx context.Context) ([]KeyRecord, error) {
	var records []KeyRecord
	err := s.db.WithContext(ctx).Order("created_at DESC").Find(&records).Error
	return records, err
}
func (s *postgresKeyStore) Get(ctx context.Context, id string) (*KeyRecord, error) {
	var record KeyRecord
	err := s.db.WithContext(ctx).First(&record, "id = ?", id).Error
	return &record, err
}
func (s *postgresKeyStore) Create(ctx context.Context, record *KeyRecord) error {
	return s.db.WithContext(ctx).Create(record).Error
}
func (s *postgresKeyStore) Update(ctx context.Context, record *KeyRecord) error {
	return s.db.WithContext(ctx).Save(record).Error
}
func (s *postgresKeyStore) Close() error { return s.sqlDB.Close() }

func OpenKeyStore(databaseURL, localPath string) (KeyStore, error) {
	if databaseURL != "" {
		return OpenPostgresKeyStore(databaseURL)
	}
	return OpenLocalKeyStore(localPath)
}

type KeyManager struct {
	Store KeyStore
	Now   func() time.Time
}

func NewKeyManager(store KeyStore) *KeyManager { return &KeyManager{Store: store, Now: time.Now} }

func validateKeyFields(name string, bucket *pb.TokenBucketConfig, expiresAt *time.Time, now time.Time) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || bucket == nil || bucket.Capacity < 1 || bucket.Capacity > 1_000_000 || bucket.RefillTokensPerSecond <= 0 || bucket.RefillTokensPerSecond > 100_000 || math.IsNaN(bucket.RefillTokensPerSecond) || math.IsInf(bucket.RefillTokensPerSecond, 0) {
		return fmt.Errorf("INVALID_INPUT: 名称或令牌桶配置无效")
	}
	if expiresAt != nil && !expiresAt.After(now) {
		return fmt.Errorf("INVALID_INPUT: 过期时间必须晚于当前时间")
	}
	return nil
}

func parseExpiry(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("INVALID_INPUT: expiresAt 必须是 RFC3339 时间")
	}
	value = value.UTC()
	return &value, nil
}

func (m *KeyManager) Create(ctx context.Context, req *pb.CreateTrainingKeyRequest) (*pb.CreateTrainingKeyResponse, error) {
	expires, err := parseExpiry(req.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if err := validateKeyFields(req.Name, req.Bucket, expires, m.Now()); err != nil {
		return nil, err
	}
	idBytes, secretBytes := make([]byte, 8), make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	if _, err := rand.Read(secretBytes); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(idBytes)
	secretPart := base64.RawURLEncoding.EncodeToString(secretBytes)
	secret := keyPrefix + "_" + id + "_" + secretPart
	sum := sha256.Sum256([]byte(secret))
	record := &KeyRecord{ID: id, Name: strings.TrimSpace(req.Name), Prefix: keyPrefix + "_" + id + "_" + secretPart[:6], SecretHash: hex.EncodeToString(sum[:]), BucketCapacity: req.Bucket.Capacity, RefillTokensPerSecond: req.Bucket.RefillTokensPerSecond, CreatedAt: m.Now().UTC(), ExpiresAt: expires}
	if err := m.Store.Create(ctx, record); err != nil {
		return nil, err
	}
	return &pb.CreateTrainingKeyResponse{Key: recordMessage(record), Secret: secret}, nil
}

func (m *KeyManager) List(ctx context.Context) (*pb.ListTrainingKeysResponse, error) {
	records, err := m.Store.List(ctx)
	if err != nil {
		return nil, err
	}
	response := &pb.ListTrainingKeysResponse{}
	for i := range records {
		response.Keys = append(response.Keys, recordMessage(&records[i]))
	}
	return response, nil
}

func (m *KeyManager) Update(ctx context.Context, id string, req *pb.UpdateTrainingKeyRequest) (*pb.TrainingKeyRecord, error) {
	record, err := m.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("NOT_FOUND: 训练 API Key 不存在")
		}
		return nil, err
	}
	expires := record.ExpiresAt
	if req.ClearExpiration {
		expires = nil
	} else if req.ExpiresAt != "" {
		expires, err = parseExpiry(req.ExpiresAt)
		if err != nil {
			return nil, err
		}
	}
	if err := validateKeyFields(req.Name, req.Bucket, expires, m.Now()); err != nil {
		return nil, err
	}
	record.Name, record.BucketCapacity, record.RefillTokensPerSecond, record.ExpiresAt = strings.TrimSpace(req.Name), req.Bucket.Capacity, req.Bucket.RefillTokensPerSecond, expires
	if err := m.Store.Update(ctx, record); err != nil {
		return nil, err
	}
	return recordMessage(record), nil
}

func (m *KeyManager) Revoke(ctx context.Context, id string) (*pb.TrainingKeyRecord, error) {
	record, err := m.Store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("NOT_FOUND: 训练 API Key 不存在")
		}
		return nil, err
	}
	if record.RevokedAt == nil {
		now := m.Now().UTC()
		record.RevokedAt = &now
		if err := m.Store.Update(ctx, record); err != nil {
			return nil, err
		}
	}
	return recordMessage(record), nil
}

func (m *KeyManager) Authenticate(ctx context.Context, secret string) (*KeyRecord, error) {
	parts := strings.SplitN(secret, "_", 3)
	if len(parts) != 3 || parts[0] != keyPrefix || len(parts[1]) != 16 {
		return nil, errors.New("UNAUTHORIZED: 训练 API Key 无效")
	}
	record, err := m.Store.Get(ctx, parts[1])
	if err != nil {
		return nil, errors.New("UNAUTHORIZED: 训练 API Key 无效")
	}
	sum := sha256.Sum256([]byte(secret))
	want, err := hex.DecodeString(record.SecretHash)
	if err != nil || subtle.ConstantTimeCompare(want, sum[:]) != 1 || record.RevokedAt != nil || record.ExpiresAt != nil && !record.ExpiresAt.After(m.Now()) {
		return nil, errors.New("UNAUTHORIZED: 训练 API Key 无效、已过期或已吊销")
	}
	return record, nil
}

func recordMessage(record *KeyRecord) *pb.TrainingKeyRecord {
	m := &pb.TrainingKeyRecord{Id: record.ID, Name: record.Name, Prefix: record.Prefix, Bucket: &pb.TokenBucketConfig{Capacity: record.BucketCapacity, RefillTokensPerSecond: record.RefillTokensPerSecond}, CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339)}
	if record.ExpiresAt != nil {
		m.ExpiresAt = record.ExpiresAt.UTC().Format(time.RFC3339)
	}
	if record.RevokedAt != nil {
		m.RevokedAt = record.RevokedAt.UTC().Format(time.RFC3339)
	}
	return m
}

type LimitResult struct {
	Allowed    bool
	Remaining  float64
	RetryAfter time.Duration
}

type BucketLimiter interface {
	Allow(context.Context, string, int64, int64, float64) (LimitResult, error)
	Reset(context.Context, string) error
}

type memoryBucket struct {
	tokens float64
	last   time.Time
}

type MemoryBucketLimiter struct {
	mu      sync.Mutex
	buckets map[string]memoryBucket
	now     func() time.Time
}

func NewMemoryBucketLimiter() *MemoryBucketLimiter {
	return &MemoryBucketLimiter{buckets: map[string]memoryBucket{}, now: time.Now}
}

func (l *MemoryBucketLimiter) Allow(_ context.Context, id string, cost, capacity int64, refill float64) (LimitResult, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[id]
	if !ok {
		b = memoryBucket{tokens: float64(capacity), last: now}
	}
	b.tokens = math.Min(float64(capacity), b.tokens+now.Sub(b.last).Seconds()*refill)
	b.last = now
	result := LimitResult{Remaining: math.Floor(b.tokens)}
	if b.tokens >= float64(cost) {
		b.tokens -= float64(cost)
		result.Allowed, result.Remaining = true, math.Floor(b.tokens)
	} else {
		result.RetryAfter = time.Duration(math.Ceil((float64(cost)-b.tokens)/refill*1000)) * time.Millisecond
	}
	l.buckets[id] = b
	return result, nil
}

func (l *MemoryBucketLimiter) Reset(_ context.Context, id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, id)
	return nil
}

type RedisBucketLimiter struct{ Client *redis.Client }

var redisBucketScript = redis.NewScript(`
local key, now, cost, capacity, refill = KEYS[1], tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4])
local values = redis.call('HMGET', key, 'tokens', 'last')
local tokens = tonumber(values[1]) or capacity
local last = tonumber(values[2]) or now
tokens = math.min(capacity, tokens + math.max(0, now-last) * refill / 1000)
local allowed = 0
if tokens >= cost then tokens = tokens-cost; allowed = 1 end
redis.call('HSET', key, 'tokens', tokens, 'last', now)
redis.call('PEXPIRE', key, math.max(60000, math.ceil(capacity/refill*2000)))
local retry = 0
if allowed == 0 then retry = math.ceil((cost-tokens)/refill*1000) end
return {allowed, tostring(tokens), retry}
`)

func (l *RedisBucketLimiter) Allow(ctx context.Context, id string, cost, capacity int64, refill float64) (LimitResult, error) {
	values, err := redisBucketScript.Run(ctx, l.Client, []string{"vorax:training:bucket:" + id}, time.Now().UnixMilli(), cost, capacity, refill).Slice()
	if err != nil || len(values) != 3 {
		return LimitResult{}, fmt.Errorf("训练限流服务不可用: %w", err)
	}
	allowed, _ := values[0].(int64)
	var tokens float64
	_, _ = fmt.Sscan(fmt.Sprint(values[1]), &tokens)
	retryMs, _ := values[2].(int64)
	return LimitResult{Allowed: allowed == 1, Remaining: math.Floor(tokens), RetryAfter: time.Duration(retryMs) * time.Millisecond}, nil
}

func (l *RedisBucketLimiter) Reset(ctx context.Context, id string) error {
	return l.Client.Del(ctx, "vorax:training:bucket:"+id).Err()
}
