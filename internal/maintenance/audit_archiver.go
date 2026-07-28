package maintenance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type AuditArchiveRepository interface {
	ArchiveAuditLogs(ctx context.Context, before time.Time, batchSize int) (int, error)
	DeleteArchivedAuditLogs(ctx context.Context, before time.Time, batchSize int) (int64, error)
}

type AuditArchiverConfig struct {
	ArchiveAfterDays int
	RetentionDays    int
	BatchSize        int
	LockKeyPrefix    string
	LockTTL          time.Duration
}

type AuditArchiveResult struct {
	Archived int64 `json:"archived"`
	Deleted  int64 `json:"deleted"`
	Skipped  bool  `json:"skipped"`
}

type AuditArchiver struct {
	repo    AuditArchiveRepository
	redis   *redis.Client
	config  AuditArchiverConfig
	lockKey string
}

var releaseAuditArchiveLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

func NewAuditArchiver(
	repo AuditArchiveRepository,
	client *redis.Client,
	config AuditArchiverConfig,
) *AuditArchiver {
	if config.ArchiveAfterDays < 1 {
		config.ArchiveAfterDays = 90
	}
	if config.BatchSize < 1 {
		config.BatchSize = 1000
	}
	if config.LockTTL <= 0 {
		config.LockTTL = 30 * time.Minute
	}
	prefix := strings.TrimSpace(config.LockKeyPrefix)
	if prefix == "" {
		prefix = "deepseek_web_api"
	}
	prefix = strings.NewReplacer("{", "_", "}", "_", " ", "_").Replace(prefix)
	return &AuditArchiver{
		repo:    repo,
		redis:   client,
		config:  config,
		lockKey: "{" + prefix + "}:audit_archive:lock",
	}
}

func (archiver *AuditArchiver) RunOnce(ctx context.Context) (AuditArchiveResult, error) {
	var result AuditArchiveResult
	token, acquired, err := archiver.acquireLock(ctx)
	if err != nil {
		return result, err
	}
	if !acquired {
		result.Skipped = true
		return result, nil
	}
	defer archiver.releaseLock(token)

	archiveBefore := time.Now().AddDate(0, 0, -archiver.config.ArchiveAfterDays)
	for {
		count, err := archiver.repo.ArchiveAuditLogs(ctx, archiveBefore, archiver.config.BatchSize)
		if err != nil {
			return result, fmt.Errorf("archive audit logs: %w", err)
		}
		result.Archived += int64(count)
		if count < archiver.config.BatchSize {
			break
		}
	}

	if archiver.config.RetentionDays > 0 {
		retentionBefore := time.Now().AddDate(0, 0, -archiver.config.RetentionDays)
		for {
			count, err := archiver.repo.DeleteArchivedAuditLogs(ctx, retentionBefore, archiver.config.BatchSize)
			if err != nil {
				return result, fmt.Errorf("delete expired audit archives: %w", err)
			}
			result.Deleted += count
			if count < int64(archiver.config.BatchSize) {
				break
			}
		}
	}
	return result, nil
}

func (archiver *AuditArchiver) acquireLock(ctx context.Context) (string, bool, error) {
	if archiver.redis == nil {
		return "", true, nil
	}
	token := uuid.NewString()
	acquired, err := archiver.redis.SetNX(ctx, archiver.lockKey, token, archiver.config.LockTTL).Result()
	return token, acquired, err
}

func (archiver *AuditArchiver) releaseLock(token string) {
	if archiver.redis == nil || token == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = releaseAuditArchiveLockScript.Run(ctx, archiver.redis, []string{archiver.lockKey}, token).Err()
}
