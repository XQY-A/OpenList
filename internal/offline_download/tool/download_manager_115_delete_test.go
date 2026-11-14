package tool

import (
	"errors"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/tache"
	"gorm.io/gorm"
)

func TestDownloadTaskManagerRestoresCloudCleanupWithoutExecution(t *testing.T) {
	_, creator := setupDownloadManagerDB(t)
	cases := []struct {
		name      string
		cloud     string
		state     tache.State
		wantCloud string
		wantState tache.State
	}{
		{name: "deleting terminal", cloud: "deleting", state: tache.StateSucceeded, wantCloud: "unknown", wantState: tache.StateSucceeded},
		{name: "deleting interrupted", cloud: "deleting", state: tache.StateRunning, wantCloud: "unknown", wantState: tache.StateFailed},
		{name: "unknown terminal", cloud: "unknown", state: tache.StateFailed, wantCloud: "unknown", wantState: tache.StateFailed},
		{name: "deleted terminal", cloud: "deleted", state: tache.StateCanceled, wantCloud: "deleted", wantState: tache.StateCanceled},
	}
	for _, tc := range cases {
		record := manager115Record(t, tc.name, creator.ID, tc.state, SubmissionAccepted)
		record.CloudDeleteState = tc.cloud
		if tc.cloud == "deleted" {
			now := time.Now()
			record.CloudDeletedAt = &now
		}
		createManager115Record(t, record)
	}
	manager, err := NewDownloadTaskManager(0, 3, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		download, ok := manager.GetByID(tc.name)
		if !ok {
			t.Fatalf("interrupted %s cleanup was lost", tc.name)
		}
		record, snapshot := manager115Snapshot(t, tc.name)
		if record.CloudDeleteState != tc.wantCloud || snapshot.State != tc.wantState || download.GetState() != tc.wantState {
			t.Fatalf("%s recovery = cloud %s, stored state %d, manager state %d", tc.name, record.CloudDeleteState, snapshot.State, download.GetState())
		}
		if record.DeletedAt != nil || record.InfoHash != "original-cloud-hash" || download.Retryable() {
			t.Fatalf("recovery lost its reference or allowed execution: %+v", record)
		}
		enqueued := false
		if err := download.RetryWith(func(*DownloadTask) { enqueued = true }); !errors.Is(err, ErrTaskConflict) || enqueued {
			t.Fatalf("%s cleanup bypassed explicit recovery through retry: %v", tc.name, err)
		}
	}
	if len(manager.GetByState(tache.StatePending, tache.StateRunning, tache.StateWaitingRetry)) != 0 {
		t.Fatal("interrupted cleanup was placed in the execution queue")
	}
}

func TestDownloadTaskManagerFailsOnCloudCleanupNormalizationWrite(t *testing.T) {
	d, creator := setupDownloadManagerDB(t)
	record := manager115Record(t, "deleting", creator.ID, tache.StateSucceeded, SubmissionAccepted)
	record.CloudDeleteState = "deleting"
	createManager115Record(t, record)
	if err := d.Callback().Update().Before("gorm:update").Register("test:fail_cloud_cleanup_recovery", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "OfflineDownload115Task" {
			tx.AddError(errors.New("cleanup normalization write failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := NewDownloadTaskManager(0, 0, false)
	if err == nil || manager != nil {
		t.Fatalf("failed cleanup normalization exposed a manager: %v", err)
	}
	stored, err := db.GetOfflineDownload115Task(record.TaskID)
	if err != nil || stored.CloudDeleteState != "deleting" || stored.DeletedAt != nil {
		t.Fatalf("failed normalization corrupted recovery evidence: %+v, %v", stored, err)
	}
}
