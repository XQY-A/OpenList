package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/tache"
)

type downloadRetryReviewTask struct {
	*DownloadTask
	runs      *atomic.Int32
	succeeded func()
}

func (t *downloadRetryReviewTask) Run() error {
	t.runs.Add(1)
	return t.DownloadTask.Run()
}

func (t *downloadRetryReviewTask) OnSucceeded() {
	if t.succeeded != nil {
		t.succeeded()
	}
}

// A marker behind the retry proves that the single worker has returned from
// both executions. Manager.Wait itself reads unsynchronized worker state.
type downloadRetryDrainTask struct {
	tache.Base
	drained chan struct{}
}

func (t *downloadRetryDrainTask) Run() error {
	close(t.drained)
	return nil
}

func waitDownloadRetryEvent(t *testing.T, event <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func TestDownload115ManualRetryDuringWorkerTerminalTail(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	manager := tache.NewManager[tache.Task](tache.WithWorks(1), tache.WithMaxRetry(0))
	terminalVisible := make(chan struct{})
	resumeWorker := make(chan struct{})
	var releaseOnce, terminalOnce, drainOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(resumeWorker) }) }
	drain := &downloadRetryDrainTask{
		Base:    tache.Base{ID: "retry-queue-drained"},
		drained: make(chan struct{}),
	}
	enqueueDrain := func() { drainOnce.Do(func() { manager.Add(drain) }) }
	t.Cleanup(func() {
		release()
		enqueueDrain()
		waitDownloadRetryEvent(t, drain.drained, "worker cleanup")
	})
	var runs atomic.Int32
	first := &downloadRetryReviewTask{
		DownloadTask: download,
		runs:         &runs,
		succeeded: func() {
			terminalOnce.Do(func() { close(terminalVisible) })
			<-resumeWorker
		},
	}
	manager.Add(first)
	waitDownloadRetryEvent(t, terminalVisible, "the previous worker's terminal state")
	if download.GetState() != tache.StateSucceeded {
		t.Fatal("worker did not expose a terminal state before its final retry check")
	}

	var next *DownloadTask
	if err := download.RetryWith(func(candidate *DownloadTask) {
		next = candidate
		if candidate == download {
			manager.Retry(download.GetID())
			return
		}
		manager.Add(&downloadRetryReviewTask{DownloadTask: candidate, runs: &runs})
	}); err != nil {
		t.Fatal(err)
	}
	if next == nil || next == download || next.GetID() != download.GetID() {
		t.Fatal("manual retry did not replace the execution while retaining its task ID")
	}
	if download.GetState() != tache.StateSucceeded || next.GetState() != tache.StatePending {
		t.Fatal("retry changed the previous worker's state or did not queue the replacement")
	}
	if snapshot := decodeDownload115Snapshot(t, store.snapshot()); snapshot.State != tache.StatePending || snapshot.Error != "" {
		t.Fatalf("replacement was not durable before enqueue: %+v", snapshot)
	}
	enqueueDrain()
	release()
	waitDownloadRetryEvent(t, drain.drained, "both executions and the retry queue to drain")

	if runs.Load() != 2 || client.submissions != 1 || len(client.lookups) != 2 {
		t.Fatalf("the old worker or retry queue executed the download again: runs=%d, submits=%d, lookups=%d", runs.Load(), client.submissions, len(client.lookups))
	}
	current, exists := manager.GetByID(download.GetID())
	if !exists || current.(*downloadRetryReviewTask).DownloadTask != next {
		t.Fatal("manager still exposes the retired execution")
	}
	if snapshot := decodeDownload115Snapshot(t, store.snapshot()); snapshot.State != tache.StateSucceeded || snapshot.Error != "" {
		t.Fatalf("old worker's trailing save overwrote the retried task: %+v", snapshot)
	}
}

func TestDownload115ManualRetryPersistenceFailureKeepsOriginal(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	download.ApiUrl = "http://localhost/api"
	initDownload115TestTask(t, download)
	if err := download.set115Submission(SubmissionAccepted, "original-receipt", client.reference); err != nil {
		t.Fatal(err)
	}
	download.SetState(tache.StateFailed)
	download.SetErr(errors.New("original failure"))
	download.cancel()
	original := store.snapshot()
	store.saveError = func(record model.OfflineDownload115Task) error {
		var snapshot download115Snapshot
		if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
			return err
		}
		if snapshot.State == tache.StatePending {
			return errors.New("cannot save retry")
		}
		return nil
	}
	enqueued := false
	if err := download.RetryWith(func(*DownloadTask) { enqueued = true }); err == nil || !strings.Contains(err.Error(), "cannot save retry") {
		t.Fatalf("retry error = %v, want replacement persistence failure", err)
	}
	if enqueued || !download.Retryable() || download.GetState() != tache.StateFailed || download.GetErr().Error() != "original failure" {
		t.Fatal("failed replacement save retired, changed, or enqueued the original task")
	}
	if record := store.snapshot(); record.Snapshot != original.Snapshot || record.InfoHash != original.InfoHash {
		t.Fatalf("failed retry changed the durable task: %+v", record)
	}
	download.SetErr(errors.New("original still writable"))
	if snapshot := decodeDownload115Snapshot(t, store.snapshot()); snapshot.Error != "original still writable" {
		t.Fatal("original task could no longer persist after a failed replacement")
	}

	store.saveError = nil
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	manager.Add(download)
	// Manager.Add creates a new context even for a terminal task. Cancel it to
	// verify that the replacement never inherits a previous execution's cancel.
	download.cancel()
	var next *DownloadTask
	if err := download.RetryWith(func(candidate *DownloadTask) {
		next = candidate
		manager.Add(candidate)
	}); err != nil {
		t.Fatal(err)
	}
	if next == nil || next == download || next.GetID() != download.GetID() || next.Open115Reference() != download.Open115Reference() {
		t.Fatal("successful replacement lost the original ID or cloud receipt")
	}
	if next.Ctx().Err() != nil || download.Ctx().Err() != context.Canceled || next.Ctx() == download.Ctx() {
		t.Fatal("retry reused the canceled execution context")
	}
	if next.Ctx().Value(conf.UserKey) != download.GetCreator() || next.Ctx().Value(conf.ApiUrlKey) != download.ApiUrl {
		t.Fatal("retry context lost its creator or API URL")
	}
	saves := store.saveCalls
	download.SetState(tache.StateSucceeded)
	download.SetErr(errors.New("late old worker error"))
	download.SetProgress(91)
	download.Cancel()
	if err := download.save115(); err != nil {
		t.Fatal(err)
	}
	if store.saveCalls != saves || next.Ctx().Err() != nil || download.GetState() != tache.StateFailed {
		t.Fatal("retired worker modified the replacement or its durable state")
	}
	if err := download.RemoveLocal(); !errors.Is(err, ErrTaskConflict) || store.deleteCalls != 0 {
		t.Fatalf("stale removal affected the replacement: err=%v, deletes=%d", err, store.deleteCalls)
	}
	if err := download.RetryWith(func(*DownloadTask) { t.Error("retired task was queued again") }); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("retired execution retry error = %v, want conflict", err)
	}
	if err := download.Run(); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("retired execution run error = %v, want conflict", err)
	}
	worker := tache.Worker[*DownloadTask]{}
	worker.Execute(next)
	if next.GetState() != tache.StateSucceeded || client.submissions != 0 || len(client.lookups) != 1 {
		t.Fatalf("replacement did not resume its original receipt: state=%v, submits=%d, lookups=%d", next.GetState(), client.submissions, len(client.lookups))
	}
}

func TestDownload115ManualRetryCanceledHonorsConfiguration(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "disabled"
		if allow {
			name = "enabled"
		}
		t.Run(name, func(t *testing.T) {
			download, store, client := newDownload115TestTask(t)
			conf.Conf.Tasks.AllowRetryCanceled = allow
			initDownload115TestTask(t, download)
			if err := download.set115Submission(SubmissionAccepted, "canceled-receipt", client.reference); err != nil {
				t.Fatal(err)
			}
			download.SetState(tache.StateCanceled)
			download.SetErr(context.Canceled)
			download.cancel()
			original := store.snapshot()
			manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
			var next *DownloadTask
			err := download.RetryWith(func(candidate *DownloadTask) {
				next = candidate
				manager.Add(candidate)
			})
			if !allow {
				if !errors.Is(err, ErrTaskConflict) || next != nil || store.snapshot().Snapshot != original.Snapshot {
					t.Fatalf("disabled canceled retry changed the task: err=%v, enqueued=%t", err, next != nil)
				}
				return
			}
			if err != nil || next == nil || next == download || next.GetState() != tache.StatePending || next.Ctx().Err() != nil {
				t.Fatalf("enabled canceled retry did not create a runnable replacement: err=%v", err)
			}
			if next.Open115Reference().InfoHash != "canceled-receipt" || client.submissions != 0 || client.cloudDeletions != 0 {
				t.Fatal("retry lost the receipt or contacted the cloud while enqueueing")
			}
		})
	}
}

func TestDownload115ManualRetryHonorsPreviouslyPersistedDeletion(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	download.SetState(tache.StateFailed)
	if err := store.Delete(download.GetID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	enqueued := false
	err := download.RetryWith(func(*DownloadTask) { enqueued = true })
	if !errors.Is(err, ErrTaskConflict) || enqueued || download.Retryable() {
		t.Fatalf("retry ignored an existing durable tombstone: err=%v, enqueued=%t, retryable=%t", err, enqueued, download.Retryable())
	}
	if store.snapshot().DeletedAt == nil || client.submissions != 0 {
		t.Fatal("retry restored or submitted a deleted task")
	}
}

func TestDownload115LocalRemovalDoesNotCancelQueuedExecution(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	manager := tache.NewManager[tache.Task](tache.WithRunning(false), tache.WithWorks(1), tache.WithMaxRetry(0))
	manager.Add(download)
	if err := download.RemoveLocal(); err != nil {
		t.Fatal(err)
	}
	manager.Remove(download.GetID())
	original := store.snapshot()
	saves := store.saveCalls
	drain := &downloadRetryDrainTask{
		Base:    tache.Base{ID: "removed-queue-drained"},
		drained: make(chan struct{}),
	}
	manager.Add(drain)
	manager.Start()
	waitDownloadRetryEvent(t, drain.drained, "the removed queued execution")
	if download.GetState() != tache.StateSucceeded || client.submissions != 1 || len(client.lookups) != 1 || client.cloudDeletions != 0 {
		t.Fatalf("local removal canceled or duplicated a queued execution: state=%v, submits=%d, lookups=%d, deletes=%d", download.GetState(), client.submissions, len(client.lookups), client.cloudDeletions)
	}
	if _, exists := manager.GetByID(download.GetID()); exists {
		t.Fatal("finished execution restored its removed manager entry")
	}
	if record := store.snapshot(); record.DeletedAt == nil || !record.DeletedAt.Equal(*original.DeletedAt) || record.Snapshot != original.Snapshot || store.saveCalls != saves {
		t.Fatalf("removed worker rewrote or restored its durable record: %+v", record)
	}
	if download.Retryable() {
		t.Fatal("removed execution became automatically retryable")
	}
	if err := download.RetryWith(func(*DownloadTask) { t.Error("removed task was enqueued again") }); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("removed execution retry error = %v, want conflict", err)
	}
}

func TestDownload115LocalRemovalDuringSubmissionStillFinishes(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	dispatched := make(chan struct{})
	allowReceipt := make(chan struct{})
	workerDone := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(allowReceipt) }) }
	client.submit = func(ctx context.Context, _ string, _ int) (string, error) {
		close(dispatched)
		select {
		case <-allowReceipt:
			return "receipt-after-local-removal", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	t.Cleanup(func() {
		release()
		waitDownloadRetryEvent(t, workerDone, "removed execution cleanup")
	})
	go func() {
		worker := tache.Worker[*DownloadTask]{}
		worker.Execute(download)
		close(workerDone)
	}()
	waitDownloadRetryEvent(t, dispatched, "cloud submission dispatch")
	if err := download.RemoveLocal(); err != nil {
		t.Fatal(err)
	}
	original := store.snapshot()
	saves := store.saveCalls
	release()
	waitDownloadRetryEvent(t, workerDone, "the receipt and completion after local removal")
	if download.GetState() != tache.StateSucceeded || download.Open115Reference().InfoHash != "receipt-after-local-removal" || download.Open115SubmissionState() != SubmissionAccepted {
		t.Fatalf("late receipt could not finish its original execution: state=%v, reference=%+v, error=%v", download.GetState(), download.Open115Reference(), download.GetErr())
	}
	if client.submissions != 1 || len(client.lookups) != 1 || client.lookups[0].InfoHash != "receipt-after-local-removal" || client.cloudDeletions != 0 {
		t.Fatalf("local removal interrupted or duplicated cloud work: submits=%d, lookups=%+v, deletes=%d", client.submissions, client.lookups, client.cloudDeletions)
	}
	if record := store.snapshot(); record.DeletedAt == nil || !record.DeletedAt.Equal(*original.DeletedAt) || record.InfoHash != original.InfoHash || record.Snapshot != original.Snapshot || store.saveCalls != saves {
		t.Fatalf("late receipt or completion overwrote the durable tombstone: %+v", record)
	}
	if download.Retryable() {
		t.Fatal("removed execution became automatically retryable after accepting its receipt")
	}
	if err := download.RetryWith(func(*DownloadTask) { t.Error("removed task was enqueued again") }); !errors.Is(err, ErrTaskConflict) {
		t.Fatalf("removed execution retry error = %v, want conflict", err)
	}
}
