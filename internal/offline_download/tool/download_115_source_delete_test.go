package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/tache"
)

func TestDownload115SourceDeletionPersistsIntentAndResult(t *testing.T) {
	for _, deleteFiles := range []bool{false, true} {
		name := "keep_files"
		if deleteFiles {
			name = "delete_files"
		}
		t.Run(name, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			client.delete = func(context.Context, Open115Reference) error {
				record := store.snapshot()
				snapshot := decodeDownload115Snapshot(t, record)
				wantState := SourceFileDeleteNone
				if deleteFiles {
					wantState = SourceFileDeleteDeleting
				}
				if record.CloudDeleteState != CloudDeleteDeleting ||
					snapshot.DeleteSourceFile != deleteFiles || snapshot.SourceFileDeleteState != wantState {
					t.Fatalf("cloud mutation preceded its durable options: %+v, %+v", record, snapshot)
				}
				return nil
			}
			result, err := download.DeleteCloudRecord(context.Background(), deleteFiles)
			wantState := SourceFileDeleteNone
			if deleteFiles {
				wantState = SourceFileDeleteDeleted
			}
			record := store.snapshot()
			snapshot := decodeDownload115Snapshot(t, record)
			if err != nil || !result.LocalDeleted || result.CloudState != CloudDeleteDeleted ||
				result.SourceFileDeleteState != wantState || record.DeletedAt == nil ||
				snapshot.DeleteSourceFile != deleteFiles || snapshot.SourceFileDeleteState != wantState {
				t.Fatalf("confirmed cleanup = %+v, snapshot = %+v, error = %v", result, snapshot, err)
			}
			if len(client.deleteSourceFiles) != 1 || client.deleteSourceFiles[0] != deleteFiles {
				t.Fatalf("cloud deletion option = %v, want %v", client.deleteSourceFiles, deleteFiles)
			}
		})
	}
}

func TestDownload115SourceDeletionCannotBeConfirmedByRecordAbsence(t *testing.T) {
	for _, phase := range []string{"already_missing", "lost_response", "lost_response_write_failed"} {
		t.Run(phase, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			wantDeletes := 0
			if phase != "already_missing" {
				wantDeletes = 1
				client.delete = func(context.Context, Open115Reference) error { return context.DeadlineExceeded }
				if phase == "lost_response_write_failed" {
					store.saveError = func(record model.OfflineDownload115Task) error {
						if record.CloudDeleteState == CloudDeleteUnknown {
							return errors.New("unknown result write failed")
						}
						return nil
					}
				}
				result, err := download.DeleteCloudRecord(context.Background(), true)
				if !errors.Is(err, context.DeadlineExceeded) || result.LocalDeleted ||
					result.CloudState != CloudDeleteUnknown || result.SourceFileDeleteState != SourceFileDeleteUnknown {
					t.Fatalf("lost response discarded the file uncertainty: %+v, %v", result, err)
				}
				store.saveError = nil
				lookups, deletions := client.calls()
				result, err = download.DeleteCloudRecord(context.Background(), false)
				var conflict *CloudDeleteError
				if !errors.As(err, &conflict) || conflict.Reason != "delete_option_conflict" || result.LocalDeleted {
					t.Fatalf("changing the saved file option was accepted: %+v, %v", result, err)
				}
				if gotLookups, gotDeletions := client.calls(); gotLookups != lookups || gotDeletions != deletions {
					t.Fatal("an option conflict queried or mutated the cloud")
				}
				result, err = download.DeleteCloudRecord(context.Background(), true)
				if !errors.As(err, &conflict) || conflict.Reason != "cloud_delete_unknown" || result.LocalDeleted {
					t.Fatalf("a still-present hash authorized repeated deletion: %+v, %v", result, err)
				}
			}
			client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
			for range 2 {
				result, err := download.DeleteCloudRecord(context.Background(), true)
				var conflict *CloudDeleteError
				if !errors.As(err, &conflict) || conflict.Reason != "source_file_delete_unconfirmed" ||
					result.LocalDeleted || result.CloudState != CloudDeleteDeleted ||
					result.SourceFileDeleteState != SourceFileDeleteUnknown {
					t.Fatalf("absence invented file deletion evidence: %+v, %v", result, err)
				}
				assertCloudDelete115Preserved(t, store)
				snapshot := decodeDownload115Snapshot(t, store.snapshot())
				if !snapshot.DeleteSourceFile || snapshot.SourceFileDeleteState != SourceFileDeleteUnknown {
					t.Fatalf("file uncertainty was not durable: %+v", snapshot)
				}
			}
			if _, deletes := client.calls(); deletes != wantDeletes {
				t.Fatalf("absence reconciliation sent %d deletions, want %d", deletes, wantDeletes)
			}
		})
	}
}

func TestDownload115SourceDeletionPersistenceFailuresKeepEvidence(t *testing.T) {
	for _, phase := range []string{"intent", "rollback", "confirmation", "tombstone"} {
		t.Run(phase, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			switch phase {
			case "intent", "rollback", "confirmation":
				failedState := map[string]string{
					"intent": CloudDeleteDeleting, "rollback": CloudDeleteNone, "confirmation": CloudDeleteDeleted,
				}[phase]
				store.saveError = func(record model.OfflineDownload115Task) error {
					if record.CloudDeleteState == failedState {
						return errors.New("test write failed")
					}
					return nil
				}
				if phase == "rollback" {
					client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
						return &Open115TaskStatus{State: 1}, nil
					}
				}
			case "tombstone":
				store.deleteErr = errors.New("test tombstone write failed")
			}
			result, err := download.DeleteCloudRecord(context.Background(), true)
			if err == nil || result.LocalDeleted {
				t.Fatalf("write failure reported full success: %+v, %v", result, err)
			}
			wantCloud, wantFiles, wantDeletes := CloudDeleteDeleted, SourceFileDeleteDeleted, 1
			switch phase {
			case "intent":
				wantCloud, wantFiles, wantDeletes = CloudDeleteNone, SourceFileDeleteNone, 0
			case "rollback":
				wantCloud, wantFiles, wantDeletes = CloudDeleteDeleting, SourceFileDeleteDeleting, 0
			}
			if result.CloudState != wantCloud || result.SourceFileDeleteState != wantFiles {
				t.Fatalf("write failure changed known evidence: %+v, want %s/%s", result, wantCloud, wantFiles)
			}
			assertCloudDelete115Preserved(t, store)
			if _, deletes := client.calls(); deletes != wantDeletes {
				t.Fatalf("failure sent %d cloud deletions, want %d", deletes, wantDeletes)
			}
			store.saveError, store.deleteErr = nil, nil
			if phase == "intent" {
				result, err = download.DeleteCloudRecord(context.Background(), true)
			} else {
				client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
				result, err = download.DeleteCloudRecord(context.Background(), true)
			}
			if phase == "rollback" {
				if err == nil || result.LocalDeleted || result.SourceFileDeleteState != SourceFileDeleteUnknown {
					t.Fatalf("failed rollback later invented file confirmation: %+v, %v", result, err)
				}
			} else if err != nil || !result.LocalDeleted || result.SourceFileDeleteState != SourceFileDeleteDeleted {
				t.Fatalf("known evidence could not finish local recovery: %+v, %v", result, err)
			}
			if _, deletes := client.calls(); (phase == "rollback" && deletes != 0) || (phase != "rollback" && deletes != 1) {
				t.Fatal("local recovery repeated a cloud mutation")
			}
		})
	}
}

func TestDownload115SourceDeletionReleasesUnusedIntent(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
		return &Open115TaskStatus{State: 1}, nil
	}
	result, err := download.DeleteCloudRecord(context.Background(), true)
	snapshot := decodeDownload115Snapshot(t, store.snapshot())
	if !errors.Is(err, ErrTaskConflict) || result.CloudState != CloudDeleteNone ||
		result.SourceFileDeleteState != SourceFileDeleteNone || snapshot.DeleteSourceFile ||
		snapshot.SourceFileDeleteState != SourceFileDeleteNone {
		t.Fatalf("unused file intent was not durably rolled back: %+v, %+v, %v", result, snapshot, err)
	}
	client.lookup = nil
	result, err = download.DeleteCloudRecord(context.Background(), false)
	if err != nil || !result.LocalDeleted || len(client.deleteSourceFiles) != 1 || client.deleteSourceFiles[0] {
		t.Fatalf("a rolled-back file option could not be changed safely: %+v, %v", result, err)
	}
}

func TestDownload115SourceDeletionKeepsReceiptAfterConcurrentTombstone(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	client.delete = func(context.Context, Open115Reference) error {
		return store.Delete(download.GetID(), time.Now())
	}
	result, err := download.DeleteCloudRecord(context.Background(), true)
	if !errors.Is(err, ErrTaskConflict) || result.LocalDeleted ||
		result.CloudState != CloudDeleteDeleted || result.SourceFileDeleteState != SourceFileDeleteDeleted {
		t.Fatalf("a concurrent tombstone discarded the confirmed cloud receipt: %+v, %v", result, err)
	}
	if _, deletes := client.calls(); deletes != 1 {
		t.Fatal("concurrent local cleanup repeated cloud deletion")
	}
}

func TestDownload115CancellationAlwaysKeepsSourceFiles(t *testing.T) {
	download, _, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	if err := download.set115Submission(SubmissionAccepted, "cancel-receipt", client.reference); err != nil {
		t.Fatal(err)
	}
	download.SetState(tache.StateRunning)
	download.cancel()
	if err := download.Run(); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation result = %v", err)
	}
	if client.cloudDeletions != 1 || client.sourceFileDeletions != 0 || client.submissions != 0 {
		t.Fatalf("cancellation changed its record-only behavior: %+v", client)
	}
}
