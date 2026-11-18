package tool

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/tache"
)

type cloudDelete115TestTool struct {
	*download115TestTool
	delete            func(context.Context, Open115Reference) error
	validateErr       error
	deleteSourceFiles []bool
}

func (f *cloudDelete115TestTool) ValidateReference(Open115Reference) error {
	return f.validateErr
}

func (f *cloudDelete115TestTool) Delete(ctx context.Context, reference Open115Reference, deleteSourceFile bool) error {
	f.mu.Lock()
	f.cloudDeletions++
	f.deleteSourceFiles = append(f.deleteSourceFiles, deleteSourceFile)
	f.mu.Unlock()
	if f.delete != nil {
		return f.delete(ctx, reference)
	}
	return nil
}

func (f *cloudDelete115TestTool) calls() (lookups, deletions int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.lookups), f.cloudDeletions
}

func newCloudDelete115TestTask(t *testing.T, id string) (*DownloadTask, *download115TestStore, *cloudDelete115TestTool) {
	t.Helper()
	download, store, base := newDownload115TestTask(t)
	client := &cloudDelete115TestTool{download115TestTool: base}
	download.tool = client
	if err := download.init115(t.Name() + "/" + id); err != nil {
		t.Fatal(err)
	}
	if err := download.set115Submission(SubmissionAccepted, "Original-Hash:+/=", base.reference); err != nil {
		t.Fatal(err)
	}
	download.SetState(tache.StateSucceeded)
	return download, store, client
}

func assertCloudDelete115Preserved(t *testing.T, store *download115TestStore) {
	t.Helper()
	record := store.snapshot()
	if record == nil || record.DeletedAt != nil || record.InfoHash != "Original-Hash:+/=" {
		t.Fatalf("failed cleanup lost its recovery record: %+v", record)
	}
}

func TestDownload115CloudDeletionRequiresTerminalCloudState(t *testing.T) {
	for _, tc := range []struct {
		name       string
		state      int
		localState tache.State
		missing    bool
		lookupErr  error
		wantDelete bool
		wantOK     bool
	}{
		{name: "completed", state: 2, wantDelete: true, wantOK: true},
		{name: "failed", state: -1, wantDelete: true, wantOK: true},
		{name: "locally failed", state: 2, localState: tache.StateFailed, wantDelete: true, wantOK: true},
		{name: "locally canceled", state: -1, localState: tache.StateCanceled, wantDelete: true, wantOK: true},
		{name: "missing", missing: true, wantOK: true},
		{name: "pending", state: 0},
		{name: "running", state: 1},
		{name: "unknown state", state: 3},
		{name: "lookup failed", lookupErr: errors.New("cloud unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			if tc.localState == tache.StateFailed || tc.localState == tache.StateCanceled {
				download.SetState(tc.localState)
			}
			client.lookup = func(_ context.Context, reference Open115Reference) (*Open115TaskStatus, error) {
				if reference != download.Open115Reference() {
					return nil, errors.New("lookup did not use the persisted cloud reference")
				}
				if tc.missing || tc.lookupErr != nil {
					return nil, tc.lookupErr
				}
				return &Open115TaskStatus{State: tc.state}, nil
			}
			result, err := download.DeleteCloudRecord(context.Background(), false)
			if (err == nil) != tc.wantOK || result.LocalDeleted != tc.wantOK {
				t.Fatalf("result = %+v, error = %v, want success %v", result, err, tc.wantOK)
			}
			lookups, deletions := client.calls()
			if lookups != 1 || (deletions == 1) != tc.wantDelete {
				t.Fatalf("cloud calls = %d lookups, %d deletions", lookups, deletions)
			}
			if tc.wantOK {
				record := store.snapshot()
				if result.CloudState != "deleted" || record.CloudDeleteState != "deleted" || record.CloudDeletedAt == nil || record.DeletedAt == nil {
					t.Fatalf("successful cleanup was not durably completed: %+v, %+v", result, record)
				}
			} else {
				assertCloudDelete115Preserved(t, store)
				if result.CloudState != CloudDeleteNone || store.snapshot().CloudDeleteState != CloudDeleteNone {
					t.Fatal("a rejected lookup did not durably release its unused deletion intent")
				}
			}
		})
	}
}

func TestDownload115CloudDeletionRejectsUnsafeLocalMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*DownloadTask, *cloudDelete115TestTool)
	}{
		{name: "pending", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.SetState(tache.StatePending) }},
		{name: "running", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.SetState(tache.StateRunning) }},
		{name: "waiting retry", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.SetState(tache.StateWaitingRetry) }},
		{name: "canceling", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.SetState(tache.StateCanceling) }},
		{name: "legacy", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.open115.SubmissionState = SubmissionLegacy }},
		{name: "missing hash", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.open115.InfoHash = "" }},
		{name: "missing storage", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.open115.StorageID = 0 }},
		{name: "missing account", change: func(d *DownloadTask, _ *cloudDelete115TestTool) { d.open115.AccountID = "" }},
		{name: "account replaced", change: func(_ *DownloadTask, c *cloudDelete115TestTool) {
			c.validateErr = errors.New("storage account changed")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			tc.change(download, client)
			result, err := download.DeleteCloudRecord(context.Background(), false)
			if err == nil || result.LocalDeleted {
				t.Fatalf("unsafe cleanup was accepted: %+v, %v", result, err)
			}
			if lookups, deletions := client.calls(); lookups != 0 || deletions != 0 {
				t.Fatalf("unsafe metadata triggered cloud calls: %d/%d", lookups, deletions)
			}
			assertCloudDelete115Preserved(t, store)
		})
	}
}

func TestDownload115CloudDeletionAllowsMissingCreator(t *testing.T) {
	download, store, _ := newCloudDelete115TestTask(t, "task")
	download.Creator = nil
	result, err := download.DeleteCloudRecord(context.Background(), false)
	if err != nil || !result.LocalDeleted || store.snapshot().DeletedAt == nil {
		t.Fatalf("administrator cleanup lost access to a deleted creator's task: %+v, %v", result, err)
	}
}

func TestDownload115CloudDeletionIntentFailurePreventsDispatch(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	store.saveError = func(record model.OfflineDownload115Task) error {
		if record.CloudDeleteState == "deleting" {
			return errors.New("intent write failed")
		}
		return nil
	}
	result, err := download.DeleteCloudRecord(context.Background(), false)
	if err == nil || result.LocalDeleted {
		t.Fatalf("intent persistence failure reported success: %+v, %v", result, err)
	}
	if lookups, deletions := client.calls(); lookups != 0 || deletions != 0 {
		t.Fatalf("cloud calls preceded the durable intent: %d lookups, %d deletions", lookups, deletions)
	}
	assertCloudDelete115Preserved(t, store)
	if store.snapshot().CloudDeleteState != "none" {
		t.Fatal("failed intent changed the durable cloud state")
	}
	store.saveError = nil
	result, err = download.DeleteCloudRecord(context.Background(), false)
	if err != nil || !result.LocalDeleted {
		t.Fatalf("retry after a pre-dispatch failure could not finish: %+v, %v", result, err)
	}
	if _, deletions := client.calls(); deletions != 1 {
		t.Fatalf("retry dispatched %d deletions, want one", deletions)
	}
}

func TestDownload115CloudDeletionUnknownResponseRechecksBeforeRecovery(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	client.delete = func(context.Context, Open115Reference) error { return context.DeadlineExceeded }
	result, err := download.DeleteCloudRecord(context.Background(), false)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || result.LocalDeleted || result.CloudState != "unknown" {
		t.Fatalf("unknown response = %+v, %v", result, err)
	}
	assertCloudDelete115Preserved(t, store)
	if store.snapshot().CloudDeleteState != "unknown" {
		t.Fatal("unknown cloud outcome was not persisted")
	}
	for _, state := range []int{2, -1, 0, 1, 3} {
		client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
			return &Open115TaskStatus{State: state}, nil
		}
		result, err = download.DeleteCloudRecord(context.Background(), false)
		var conflict *CloudDeleteError
		if !errors.Is(err, ErrTaskConflict) || !errors.As(err, &conflict) || conflict.Reason != "cloud_delete_unknown" || result.LocalDeleted {
			t.Fatalf("unknown outcome with cloud state %d was not refused: %+v, %v", state, result, err)
		}
		if result.CloudState != CloudDeleteUnknown || store.snapshot().CloudDeleteState != CloudDeleteUnknown {
			t.Fatal("querying an existing record discarded the unknown deletion outcome")
		}
		if _, deletions := client.calls(); deletions != 1 {
			t.Fatalf("unknown outcome repeated the cloud deletion %d times", deletions)
		}
	}
	client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
	result, err = download.DeleteCloudRecord(context.Background(), false)
	if err != nil || !result.LocalDeleted || result.CloudState != "deleted" {
		t.Fatalf("reliable absence did not complete recovery: %+v, %v", result, err)
	}
	if lookups, deletions := client.calls(); lookups != 7 || deletions != 1 {
		t.Fatalf("recovery repeated the cloud write: %d lookups, %d deletions", lookups, deletions)
	}
}

func TestDownload115CloudDeletionUnusedIntentRollback(t *testing.T) {
	for _, rejected := range []struct {
		name      string
		state     int
		lookupErr error
	}{
		{name: "active", state: 1},
		{name: "unrecognized", state: 3},
		{name: "query failed", lookupErr: errors.New("query unavailable")},
	} {
		for _, failRollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/rollback_fails=%t", rejected.name, failRollback), func(t *testing.T) {
				download, store, client := newCloudDelete115TestTask(t, "task")
				client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
					if store.snapshot().CloudDeleteState != CloudDeleteDeleting {
						return nil, errors.New("lookup preceded its durable intent")
					}
					return &Open115TaskStatus{State: rejected.state}, rejected.lookupErr
				}
				if failRollback {
					store.saveError = func(record model.OfflineDownload115Task) error {
						if record.CloudDeleteState == CloudDeleteNone {
							if download.cloudDeleteResult().CloudState != CloudDeleteDeleting {
								t.Error("rollback published none before its write completed")
							}
							return errors.New("rollback write failed")
						}
						return nil
					}
				}
				result, err := download.DeleteCloudRecord(context.Background(), false)
				if err == nil || result.LocalDeleted {
					t.Fatalf("rejected cloud query removed the task: %+v, %v", result, err)
				}
				wantState := CloudDeleteNone
				if failRollback {
					wantState = CloudDeleteDeleting
					var failure *CloudDeleteError
					if !errors.As(err, &failure) || failure.Reason != "persistence_failed" {
						t.Fatalf("rollback write failure lost its reason: %v", err)
					}
				}
				if result.CloudState != wantState || store.snapshot().CloudDeleteState != wantState {
					t.Fatalf("rollback state = %+v, stored = %+v", result, store.snapshot())
				}
				if lookups, deletes := client.calls(); lookups != 1 || deletes != 0 {
					t.Fatalf("rejected cloud query triggered unexpected calls: %d/%d", lookups, deletes)
				}
				store.saveError = nil
				client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) {
					return &Open115TaskStatus{State: 2}, nil
				}
				result, err = download.DeleteCloudRecord(context.Background(), false)
				if failRollback {
					var conflict *CloudDeleteError
					if !errors.Is(err, ErrTaskConflict) || !errors.As(err, &conflict) || conflict.Reason != "cloud_delete_unknown" || result.LocalDeleted {
						t.Fatalf("failed rollback authorized another deletion attempt: %+v, %v", result, err)
					}
					client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
					result, err = download.DeleteCloudRecord(context.Background(), false)
				}
				if err != nil || !result.LocalDeleted || store.snapshot().DeletedAt == nil {
					t.Fatalf("reconciliation did not finish: %+v, %v", result, err)
				}
				_, deletes := client.calls()
				if (failRollback && deletes != 0) || (!failRollback && deletes != 1) {
					t.Fatalf("cloud deletion count = %d after rollback failure %v", deletes, failRollback)
				}
			})
		}
	}
}

func TestDownload115CloudDeletionPreservesConfirmedDeletionAfterStoreFailure(t *testing.T) {
	for _, phase := range []string{"cloud receipt", "local tombstone"} {
		t.Run(phase, func(t *testing.T) {
			download, store, client := newCloudDelete115TestTask(t, "task")
			if phase == "cloud receipt" {
				store.saveError = func(record model.OfflineDownload115Task) error {
					if record.CloudDeleteState == "deleted" {
						return errors.New("receipt write failed")
					}
					return nil
				}
			} else {
				store.deleteErr = errors.New("tombstone write failed")
			}
			result, err := download.DeleteCloudRecord(context.Background(), false)
			if err == nil || result.LocalDeleted || result.CloudState != "deleted" {
				t.Fatalf("post-cloud persistence failure = %+v, %v", result, err)
			}
			assertCloudDelete115Preserved(t, store)
			store.saveError, store.deleteErr = nil, nil
			result, err = download.DeleteCloudRecord(context.Background(), false)
			if err == nil || result.LocalDeleted {
				t.Fatalf("a reappeared hash was treated as the deleted task: %+v, %v", result, err)
			}
			var conflict *CloudDeleteError
			if !errors.As(err, &conflict) || conflict.Reason != "cloud_record_reappeared" || !errors.Is(err, ErrTaskConflict) {
				t.Fatalf("reappeared cloud record did not return its stable conflict reason: %v", err)
			}
			if _, deletions := client.calls(); deletions != 1 {
				t.Fatalf("confirmed deletion was repeated %d times", deletions)
			}
			assertCloudDelete115Preserved(t, store)
			client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
			result, err = download.DeleteCloudRecord(context.Background(), false)
			if err != nil || !result.LocalDeleted || store.snapshot().DeletedAt == nil {
				t.Fatalf("recovery did not persist the final tombstone: %+v, %v", result, err)
			}
			if _, deletions := client.calls(); deletions != 1 {
				t.Fatal("recovery sent another cloud deletion")
			}
		})
	}
}

type cloudDelete115Outcome struct {
	result CloudDeleteResult
	err    error
}

func waitCloudDelete115(t *testing.T, result <-chan cloudDelete115Outcome) cloudDelete115Outcome {
	t.Helper()
	select {
	case outcome := <-result:
		return outcome
	case <-time.After(3 * time.Second):
		t.Fatal("cloud cleanup did not finish")
		return cloudDelete115Outcome{}
	}
}

func cleanupCloudDelete115Worker(t *testing.T, exited <-chan struct{}, release func()) {
	t.Helper()
	t.Cleanup(func() {
		release()
		select {
		case <-exited:
		case <-time.After(3 * time.Second):
			t.Error("cloud cleanup test worker did not exit")
		}
	})
}

func TestDownload115CloudDeletionSerializesTaskRetryAndMetadataReads(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client.delete = func(ctx context.Context, _ Open115Reference) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan cloudDelete115Outcome, 1)
	exited := make(chan struct{})
	cleanupCloudDelete115Worker(t, exited, unblock)
	go func() {
		defer close(exited)
		result, err := download.DeleteCloudRecord(context.Background(), false)
		done <- cloudDelete115Outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first deletion did not reach the cloud")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := download.DeleteCloudRecord(ctx, false)
	if !errors.Is(err, context.DeadlineExceeded) || result.LocalDeleted {
		t.Fatalf("same-task lock ignored request cancellation: %+v, %v", result, err)
	}
	var enqueued atomic.Int32
	retryDone := make(chan error, 1)
	retryExited := make(chan struct{})
	cleanupCloudDelete115Worker(t, retryExited, unblock)
	go func() {
		defer close(retryExited)
		retryDone <- download.RetryWith(func(*DownloadTask) { enqueued.Add(1) })
	}()
	readDone := make(chan struct{})
	cleanupCloudDelete115Worker(t, readDone, unblock)
	go func() {
		for i := 0; i < 100; i++ {
			_ = download.Get115TaskInfo()
			_ = download.Open115Reference()
			_ = download.GetState()
		}
		close(readDone)
	}()
	unblock()
	outcome := waitCloudDelete115(t, done)
	if outcome.err != nil || !outcome.result.LocalDeleted || store.snapshot().DeletedAt == nil {
		t.Fatalf("first deletion failed: %+v, %v", outcome.result, outcome.err)
	}
	select {
	case err := <-retryDone:
		if !errors.Is(err, ErrTaskConflict) || enqueued.Load() != 0 {
			t.Fatalf("concurrent retry escaped cloud deletion: %v, queued %d", err, enqueued.Load())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("retry did not release the operation lock")
	}
	select {
	case <-readDone:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata reads blocked indefinitely")
	}
	if lookups, deletions := client.calls(); lookups != 1 || deletions != 1 {
		t.Fatalf("metadata or duplicate operations touched the cloud: %d/%d", lookups, deletions)
	}
}

func TestDownload115CloudDeletionWaitsForSameAccountSubmission(t *testing.T) {
	pending, _, submitting := newDownload115TestTask(t)
	initDownload115TestTask(t, pending)
	download, store, client := newCloudDelete115TestTask(t, "cleanup")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	submitting.submit = func(ctx context.Context, _ string, _ int) (string, error) {
		close(entered)
		select {
		case <-release:
			return "submitted-hash", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	runDone := make(chan error, 1)
	runExited := make(chan struct{})
	cleanupCloudDelete115Worker(t, runExited, unblock)
	go func() {
		defer close(runExited)
		runDone <- pending.run115()
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("submission did not acquire the account lock")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	result, err := download.DeleteCloudRecord(ctx, false)
	if !errors.Is(err, context.DeadlineExceeded) || result.LocalDeleted {
		t.Fatalf("account-lock wait ignored cancellation: %+v, %v", result, err)
	}
	if lookups, deletions := client.calls(); lookups != 0 || deletions != 0 {
		t.Fatalf("cleanup overlapped another submission: %d/%d", lookups, deletions)
	}
	assertCloudDelete115Preserved(t, store)
	unblock()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("submission did not release its account lock")
	}
	result, err = download.DeleteCloudRecord(context.Background(), false)
	if err != nil || !result.LocalDeleted {
		t.Fatalf("canceled waiter leaked the account lock: %+v, %v", result, err)
	}
}

func TestDownload115CloudDeletionHonorsTombstoneWrittenDuringLookup(t *testing.T) {
	download, store, client := newCloudDelete115TestTask(t, "task")
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	client.lookup = func(ctx context.Context, _ Open115Reference) (*Open115TaskStatus, error) {
		close(entered)
		select {
		case <-release:
			return &Open115TaskStatus{State: 2}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	done := make(chan cloudDelete115Outcome, 1)
	exited := make(chan struct{})
	cleanupCloudDelete115Worker(t, exited, unblock)
	go func() {
		defer close(exited)
		result, err := download.DeleteCloudRecord(context.Background(), false)
		done <- cloudDelete115Outcome{result, err}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup did not reach lookup")
	}
	if err := store.Delete(download.GetID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	unblock()
	_ = waitCloudDelete115(t, done)
	if _, deletions := client.calls(); deletions != 0 {
		t.Fatal("a previously removed task dispatched a cloud deletion")
	}
	if err := download.save115(); err != nil {
		t.Fatal(err)
	}
	record := store.snapshot()
	if record.DeletedAt == nil || record.CloudDeleteState != CloudDeleteDeleting {
		t.Fatalf("late cleanup resurrected or rewrote the tombstone: %+v", record)
	}
	if download.Retryable() {
		t.Fatal("removed task remained retryable")
	}
}
