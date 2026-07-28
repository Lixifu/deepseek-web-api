package maintenance

import (
	"context"
	"testing"
	"time"
)

type fakeAuditArchiveRepository struct {
	archiveResults []int
	deleteResults  []int64
	archiveCalls   int
	deleteCalls    int
}

func (repo *fakeAuditArchiveRepository) ArchiveAuditLogs(context.Context, time.Time, int) (int, error) {
	result := repo.archiveResults[repo.archiveCalls]
	repo.archiveCalls++
	return result, nil
}

func (repo *fakeAuditArchiveRepository) DeleteArchivedAuditLogs(context.Context, time.Time, int) (int64, error) {
	result := repo.deleteResults[repo.deleteCalls]
	repo.deleteCalls++
	return result, nil
}

func TestAuditArchiverProcessesAllBatches(t *testing.T) {
	repo := &fakeAuditArchiveRepository{
		archiveResults: []int{2, 1},
		deleteResults:  []int64{2, 0},
	}
	archiver := NewAuditArchiver(repo, nil, AuditArchiverConfig{
		ArchiveAfterDays: 90,
		RetentionDays:    365,
		BatchSize:        2,
	})
	result, err := archiver.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Archived != 3 || result.Deleted != 2 {
		t.Fatalf("RunOnce() = %#v", result)
	}
	if repo.archiveCalls != 2 || repo.deleteCalls != 2 {
		t.Fatalf("calls archive=%d delete=%d", repo.archiveCalls, repo.deleteCalls)
	}
}
