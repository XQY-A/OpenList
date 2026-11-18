package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/tache"
	"gorm.io/gorm"
)

func TestDownloadTaskManagerRestoresSourceDeletionWithoutExecution(t *testing.T) {
	_, creator := setupDownloadManagerDB(t)
	client := &cloudDelete115TestTool{download115TestTool: &download115TestTool{}}
	previous, existed := Tools[Open115Name]
	Tools[Open115Name] = client
	t.Cleanup(func() {
		if existed {
			Tools[Open115Name] = previous
		} else {
			delete(Tools, Open115Name)
		}
	})
	cases := []struct {
		name       string
		cloud      string
		files      string
		deleteFile bool
		wantCloud  string
		wantFiles  string
		canFiles   bool
	}{
		{name: "old_snapshot", cloud: CloudDeleteNone, wantCloud: CloudDeleteNone, wantFiles: SourceFileDeleteNone, canFiles: true},
		{name: "keep_files_confirmed", cloud: CloudDeleteDeleted, wantCloud: CloudDeleteDeleted, wantFiles: SourceFileDeleteNone},
		{name: "delete_files_interrupted", cloud: CloudDeleteDeleting, files: SourceFileDeleteDeleting, deleteFile: true, wantCloud: CloudDeleteUnknown, wantFiles: SourceFileDeleteUnknown, canFiles: true},
		{name: "delete_files_missing_state", cloud: CloudDeleteUnknown, deleteFile: true, wantCloud: CloudDeleteUnknown, wantFiles: SourceFileDeleteUnknown, canFiles: true},
		{name: "delete_files_unknown", cloud: CloudDeleteDeleted, files: SourceFileDeleteUnknown, deleteFile: true, wantCloud: CloudDeleteDeleted, wantFiles: SourceFileDeleteUnknown, canFiles: true},
		{name: "delete_files_confirmed", cloud: CloudDeleteDeleted, files: SourceFileDeleteDeleted, deleteFile: true, wantCloud: CloudDeleteDeleted, wantFiles: SourceFileDeleteDeleted, canFiles: true},
	}
	for _, tc := range cases {
		record := manager115Record(t, tc.name, creator.ID, tache.StateSucceeded, SubmissionAccepted)
		record.CloudDeleteState = tc.cloud
		var snapshot download115Snapshot
		if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
			t.Fatal(err)
		}
		snapshot.DeleteSourceFile, snapshot.SourceFileDeleteState = tc.deleteFile, tc.files
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		record.Snapshot = string(encoded)
		createManager115Record(t, record)
	}
	manager, err := NewDownloadTaskManager(0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		download, ok := manager.GetByID(tc.name)
		if !ok || download.GetState() != tache.StateSucceeded {
			t.Fatalf("recovery lost %s or changed its terminal state", tc.name)
		}
		record, snapshot := manager115Snapshot(t, tc.name)
		info := download.Get115TaskInfo()
		if record.CloudDeleteState != tc.wantCloud || snapshot.DeleteSourceFile != tc.deleteFile ||
			info.SourceFileDeleteState != tc.wantFiles || info.CanDeleteSourceFile != tc.canFiles {
			t.Fatalf("%s recovery = %+v, %+v", tc.name, info, snapshot)
		}
		if tc.deleteFile && snapshot.SourceFileDeleteState != tc.wantFiles {
			t.Fatalf("%s did not persist the normalized file outcome: %+v", tc.name, snapshot)
		}
		if tc.cloud != CloudDeleteNone && download.Retryable() {
			t.Fatalf("%s cleanup could return to download execution", tc.name)
		}
	}
	if len(manager.GetByState(tache.StatePending, tache.StateRunning, tache.StateWaitingRetry)) != 0 {
		t.Fatal("recovery queued a source file deletion")
	}
	if lookups, deletions := client.calls(); lookups != 0 || deletions != 0 {
		t.Fatalf("recovery contacted the cloud: %d/%d", lookups, deletions)
	}
}

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

func TestDownloadTaskManagerCloudCleanupWriteFailureCannotDeleteReappearedHash(t *testing.T) {
	for _, initiallyMissing := range []bool{false, true} {
		name := "delete_succeeded"
		if initiallyMissing {
			name = "first_lookup_missing"
		}
		t.Run(name, func(t *testing.T) {
			database, creator := setupDownloadManagerDB(t)
			record := manager115Record(t, "cleanup-write-failed", creator.ID, tache.StateSucceeded, SubmissionAccepted)
			createManager115Record(t, record)
			client := &cloudDelete115TestTool{download115TestTool: &download115TestTool{
				reference: Open115Reference{record.StorageID, record.AccountID, record.InfoHash},
			}}
			previous, existed := Tools[Open115Name]
			Tools[Open115Name] = client
			t.Cleanup(func() {
				if existed {
					Tools[Open115Name] = previous
				} else {
					delete(Tools, Open115Name)
				}
			})
			if initiallyMissing {
				client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
					return nil, nil
				}
			}
			var rejectConfirmation atomic.Bool
			rejectConfirmation.Store(true)
			callback := "test:fail_cloud_cleanup_confirmation"
			if err := database.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
				update, ok := tx.Statement.Dest.(*model.OfflineDownload115Task)
				if ok && update.TaskID == record.TaskID && update.CloudDeleteState == CloudDeleteDeleted && rejectConfirmation.Load() {
					tx.AddError(errors.New("injected cloud deletion confirmation write failure"))
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := database.Callback().Update().Remove(callback); err != nil {
					t.Errorf("remove confirmation failure callback: %v", err)
				}
			})
			manager, err := NewDownloadTaskManager(1, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			download, ok := manager.GetByID(record.TaskID)
			if !ok {
				t.Fatal("initial task was not restored")
			}
			result, err := download.DeleteCloudRecord(context.Background(), false)
			var persistenceErr *CloudDeleteError
			if !errors.As(err, &persistenceErr) || persistenceErr.Reason != "persistence_failed" || result.LocalDeleted || result.CloudState != CloudDeleteDeleted {
				t.Fatalf("confirmation write failure = %+v, %v", result, err)
			}
			stored, _ := manager115Snapshot(t, record.TaskID)
			if stored.CloudDeleteState != CloudDeleteDeleting || stored.DeletedAt != nil || stored.CloudDeletedAt != nil {
				t.Fatalf("failed confirmation lost its pre-lookup durable intent: %+v", stored)
			}
			wantDeletions := 1
			if initiallyMissing {
				wantDeletions = 0
			}
			if lookups, deletions := client.calls(); lookups != 1 || deletions != wantDeletions {
				t.Fatalf("initial cloud calls = %d/%d, want 1/%d", lookups, deletions, wantDeletions)
			}
			rejectConfirmation.Store(false)

			// A later record with the same hash may be either complete or
			// active. Freshly recovered instances cannot establish its identity
			// from the hash, so neither state authorizes another deletion.
			for index, cloudState := range []int{2, 1} {
				client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
					return &Open115TaskStatus{State: cloudState}, nil
				}
				restarted, err := NewDownloadTaskManager(1, 0, false)
				if err != nil {
					t.Fatal(err)
				}
				restored, ok := restarted.GetByID(record.TaskID)
				if !ok || restored == download || restored.GetState() != tache.StateSucceeded || restored.Retryable() {
					t.Fatal("restart lost the terminal task or allowed it to execute")
				}
				restarted.Start()
				restarted.Pause()
				if lookups, deletions := client.calls(); lookups != index+1 || deletions != wantDeletions {
					t.Fatalf("restart touched the cloud: %d/%d", lookups, deletions)
				}
				stored, _ = manager115Snapshot(t, record.TaskID)
				if stored.CloudDeleteState != CloudDeleteUnknown || stored.InfoHash != record.InfoHash || stored.DeletedAt != nil {
					t.Fatalf("restart lost its conservative cleanup state: %+v", stored)
				}
				result, err = restored.DeleteCloudRecord(context.Background(), false)
				var conflict *CloudDeleteError
				if !errors.Is(err, ErrTaskConflict) || !errors.As(err, &conflict) || conflict.Reason != "cloud_delete_unknown" || result.LocalDeleted || result.CloudState != CloudDeleteUnknown {
					t.Fatalf("restarted cleanup accepted a same-hash record in state %d: %+v, %v", cloudState, result, err)
				}
				if lookups, deletions := client.calls(); lookups != index+2 || deletions != wantDeletions {
					t.Fatalf("reconciliation repeated a deletion: %d/%d", lookups, deletions)
				}
				stored, _ = manager115Snapshot(t, record.TaskID)
				if stored.DeletedAt != nil || stored.CloudDeleteState != CloudDeleteUnknown {
					t.Fatalf("rejected reconciliation discarded its recovery state: %+v", stored)
				}
			}
			client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
			restarted, err := NewDownloadTaskManager(1, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			restored, ok := restarted.GetByID(record.TaskID)
			if !ok {
				t.Fatal("task disappeared before reliable absence was confirmed")
			}
			result, err = restored.DeleteCloudRecord(context.Background(), false)
			if err != nil || !result.LocalDeleted || result.CloudState != CloudDeleteDeleted {
				t.Fatalf("reliable absence did not finish local cleanup: %+v, %v", result, err)
			}
			if lookups, deletions := client.calls(); lookups != 4 || deletions != wantDeletions {
				t.Fatalf("final reconciliation repeated a cloud mutation: %d/%d", lookups, deletions)
			}
			stored, _ = manager115Snapshot(t, record.TaskID)
			if stored.DeletedAt == nil || stored.CloudDeleteState != CloudDeleteDeleted || stored.CloudDeletedAt == nil {
				t.Fatalf("final tombstone was not persisted: %+v", stored)
			}
			afterRemoval, err := NewDownloadTaskManager(1, 0, false)
			if err != nil || len(afterRemoval.GetAll()) != 0 {
				t.Fatalf("final recovery revived the removed task: %v", err)
			}
		})
	}
}
