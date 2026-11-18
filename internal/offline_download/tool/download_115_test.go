package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
)

type download115TestStore struct {
	mu          sync.Mutex
	record      *model.OfflineDownload115Task
	createErr   error
	deleteErr   error
	saveError   func(model.OfflineDownload115Task) error
	createCalls int
	saveCalls   int
	deleteCalls int
}

func cloneDownload115Record(record *model.OfflineDownload115Task) *model.OfflineDownload115Task {
	if record == nil {
		return nil
	}
	copy := *record
	copy.AcceptedAt = copyTime(record.AcceptedAt)
	copy.CloudDeletedAt = copyTime(record.CloudDeletedAt)
	copy.DeletedAt = copyTime(record.DeletedAt)
	return &copy
}

func (s *download115TestStore) Create(record *model.OfflineDownload115Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	if s.createErr != nil {
		return s.createErr
	}
	if s.record != nil {
		return errors.New("duplicate task")
	}
	s.record = cloneDownload115Record(record)
	return nil
}

func (s *download115TestStore) Save(record *model.OfflineDownload115Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls++
	if s.record == nil {
		return errors.New("task was not created")
	}
	if s.record.DeletedAt != nil {
		return db.ErrOfflineDownload115TaskDeleted
	}
	if s.saveError != nil {
		if err := s.saveError(*record); err != nil {
			return err
		}
	}
	s.record = cloneDownload115Record(record)
	return nil
}

func (s *download115TestStore) Delete(id string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	if s.deleteErr != nil {
		return s.deleteErr
	}
	if s.record == nil || s.record.TaskID != id {
		return errors.New("task not found")
	}
	if s.record.DeletedAt == nil {
		s.record.DeletedAt = &at
	}
	return nil
}

func (s *download115TestStore) snapshot() *model.OfflineDownload115Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneDownload115Record(s.record)
}

type download115TestTool struct {
	mu                  sync.Mutex
	reference           Open115Reference
	prepareErr          error
	submit              func(context.Context, string, int) (string, error)
	lookup              func(context.Context, Open115Reference) (*Open115TaskStatus, error)
	submissions         int
	lookups             []Open115Reference
	preparedPaths       []string
	genericCalls        int
	cloudDeletions      int
	sourceFileDeletions int
}

func (f *download115TestTool) Name() string                             { return Open115Name }
func (f *download115TestTool) Items() []model.SettingItem               { return nil }
func (f *download115TestTool) Init() (string, error)                    { return "ok", nil }
func (f *download115TestTool) IsReady() bool                            { return true }
func (f *download115TestTool) ValidateReference(Open115Reference) error { return nil }

func (f *download115TestTool) unexpectedGenericCall() error {
	f.mu.Lock()
	f.genericCalls++
	f.mu.Unlock()
	return errors.New("unexpected generic offline tool call")
}

func (f *download115TestTool) Run(*DownloadTask) error {
	return f.unexpectedGenericCall()
}

func (f *download115TestTool) AddURL(*AddUrlArgs) (string, error) {
	return "", f.unexpectedGenericCall()
}

func (f *download115TestTool) Remove(*DownloadTask) error {
	return f.unexpectedGenericCall()
}

func (f *download115TestTool) Status(*DownloadTask) (*Status, error) {
	return nil, f.unexpectedGenericCall()
}

func (f *download115TestTool) Prepare(_ context.Context, path string) (*Open115Submission, error) {
	f.mu.Lock()
	f.preparedPaths = append(f.preparedPaths, path)
	reference, err := f.reference, f.prepareErr
	f.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Open115Submission{
		Reference: reference,
		Submit: func(ctx context.Context, url string) (string, error) {
			f.mu.Lock()
			f.submissions++
			attempt, submit := f.submissions, f.submit
			f.mu.Unlock()
			if submit != nil {
				return submit(ctx, url, attempt)
			}
			return "Cloud-ReCeIpT:+/=", nil
		},
	}, nil
}

func (f *download115TestTool) Lookup(ctx context.Context, reference Open115Reference) (*Open115TaskStatus, error) {
	f.mu.Lock()
	f.lookups = append(f.lookups, reference)
	lookup := f.lookup
	f.mu.Unlock()
	if lookup != nil {
		return lookup(ctx, reference)
	}
	return &Open115TaskStatus{
		Status: Status{Completed: true, Progress: 100, TotalBytes: 123, Status: "completed"},
		State:  2,
	}, nil
}

func (f *download115TestTool) Delete(_ context.Context, _ Open115Reference, deleteSourceFile bool) error {
	f.mu.Lock()
	f.cloudDeletions++
	if deleteSourceFile {
		f.sourceFileDeletions++
	}
	f.mu.Unlock()
	return nil
}

func newDownload115TestTask(t *testing.T) (*DownloadTask, *download115TestStore, *download115TestTool) {
	t.Helper()
	previous := conf.Conf
	conf.Conf = conf.DefaultConfig(t.TempDir())
	conf.Conf.Tasks.Download.MaxRetry = 0
	t.Cleanup(func() { conf.Conf = previous })
	store := &download115TestStore{}
	client := &download115TestTool{reference: Open115Reference{StorageID: 41, AccountID: "account-115"}}
	download := &DownloadTask{
		TaskExtension: task.TaskExtension{Creator: &model.User{ID: 17, Username: "owner", Password: "must-not-persist"}},
		Url:           "ed2k://test-url",
		DstDirPath:    "/115/downloads",
		TempDir:       "/115/downloads",
		DeletePolicy:  DeleteNever,
		Toolname:      Open115Name,
		tool:          client,
		open115Store:  store,
	}
	ctx, cancel := context.WithCancel(context.Background())
	download.SetCtx(ctx)
	download.SetCancelFunc(cancel)
	t.Cleanup(cancel)
	return download, store, client
}

func initDownload115TestTask(t *testing.T, download *DownloadTask) {
	t.Helper()
	if err := download.init115(t.Name()); err != nil {
		t.Fatal(err)
	}
}

func decodeDownload115Snapshot(t *testing.T, record *model.OfflineDownload115Task) download115Snapshot {
	t.Helper()
	if record == nil {
		t.Fatal("task has no persisted record")
	}
	var snapshot download115Snapshot
	if err := json.Unmarshal([]byte(record.Snapshot), &snapshot); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestDownload115InitialPersistenceFailurePreventsSubmission(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	store.createErr = errors.New("disk unavailable")
	if err := download.init115("initial-write-failure"); err == nil || !strings.Contains(err.Error(), "disk unavailable") {
		t.Fatalf("init error = %v, want persistence failure", err)
	}
	if store.snapshot() != nil || store.createCalls != 1 || client.submissions != 0 || client.genericCalls != 0 {
		t.Fatal("initial persistence failure created or submitted a task")
	}
}

func TestDownload115SubmittingPersistenceFailurePreventsDispatch(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	store.saveError = func(record model.OfflineDownload115Task) error {
		if record.SubmissionState == SubmissionSubmitting {
			return errors.New("cannot save submission intent")
		}
		return nil
	}
	if err := download.Run(); err == nil || !strings.Contains(err.Error(), "cannot save submission intent") {
		t.Fatalf("run error = %v, want persistence failure", err)
	}
	if client.submissions != 0 || client.genericCalls != 0 || len(client.lookups) != 0 {
		t.Fatal("cloud request was sent before submission intent was saved")
	}
	if !download.Retryable() {
		t.Fatal("failure before dispatch should remain retryable")
	}
}

func TestDownload115PersistsCloudReceiptAndActualBinding(t *testing.T) {
	for _, persistent := range []bool{false, true} {
		t.Run(fmt.Sprintf("generic_persistence_%t", persistent), func(t *testing.T) {
			download, store, client := newDownload115TestTask(t)
			conf.Conf.Tasks.Download.TaskPersistant = persistent
			initDownload115TestTask(t, download)
			client.submit = func(ctx context.Context, url string, _ int) (string, error) {
				record := store.snapshot()
				if record.SubmissionState != SubmissionSubmitting || record.StorageID != 41 || record.AccountID != "account-115" {
					t.Errorf("cloud request preceded durable binding: %+v", record)
				}
				if url != download.Url {
					t.Errorf("submitted URL = %q, want %q", url, download.Url)
				}
				if _, ok := ctx.Deadline(); !ok {
					t.Error("submission context has no timeout")
				}
				return "Cloud-ReCeIpT:+/=", nil
			}
			worker := tache.Worker[*DownloadTask]{}
			worker.Execute(download)
			record := store.snapshot()
			snapshot := decodeDownload115Snapshot(t, record)
			if download.GetState() != tache.StateSucceeded || snapshot.State != tache.StateSucceeded || snapshot.EndTime == nil {
				t.Fatalf("successful task did not persist its terminal snapshot: %+v", snapshot)
			}
			if record.InfoHash != "Cloud-ReCeIpT:+/=" || record.SubmissionState != SubmissionAccepted || record.AcceptedAt == nil {
				t.Fatalf("cloud receipt was lost or normalized: %+v", record)
			}
			if record.CreatorID != 17 || strings.Contains(record.Snapshot, "must-not-persist") || strings.Contains(record.Snapshot, "owner") {
				t.Fatalf("snapshot did not keep creator identity separate: %+v", record)
			}
			if download.Persistable() {
				t.Fatal("115 task would also be written to generic JSON persistence")
			}
			if client.submissions != 1 || client.genericCalls != 0 || len(client.lookups) != 1 || client.lookups[0] != download.Open115Reference() {
				t.Fatalf("unexpected submission/query path: submissions=%d, lookups=%+v", client.submissions, client.lookups)
			}
			if len(client.preparedPaths) != 1 || client.preparedPaths[0] != download.TempDir || snapshot.TotalBytes != 123 {
				t.Fatalf("wrong preparation path or progress snapshot: paths=%v, snapshot=%+v", client.preparedPaths, snapshot)
			}
		})
	}
}

func TestDownload115RetryAfterReceiptWriteFailureDoesNotResubmit(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	var failAccepted atomic.Bool
	failAccepted.Store(true)
	store.saveError = func(record model.OfflineDownload115Task) error {
		if record.SubmissionState == SubmissionAccepted && failAccepted.Load() {
			return errors.New("receipt write failed")
		}
		return nil
	}
	worker := tache.Worker[*DownloadTask]{}
	worker.Execute(download)
	if download.GetState() != tache.StateFailed || download.GetErr() == nil || !strings.Contains(download.GetErr().Error(), "receipt write failed") {
		t.Fatalf("failed receipt save was hidden: state=%v, error=%v", download.GetState(), download.GetErr())
	}
	if download.Open115Reference().InfoHash != "Cloud-ReCeIpT:+/=" || download.Open115SubmissionState() != SubmissionAccepted {
		t.Fatal("accepted cloud receipt was discarded from memory")
	}
	if client.submissions != 1 || len(client.lookups) != 0 || store.snapshot().SubmissionState != SubmissionSubmitting {
		t.Fatal("task queried or resubmitted before receipt became durable")
	}
	failAccepted.Store(false)
	client.lookup = func(_ context.Context, reference Open115Reference) (*Open115TaskStatus, error) {
		if record := store.snapshot(); record.InfoHash != reference.InfoHash || record.SubmissionState != SubmissionAccepted {
			t.Errorf("query preceded saving the in-memory receipt: %+v", record)
		}
		return &Open115TaskStatus{Status: Status{Completed: true}, State: 2}, nil
	}
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	var retried *DownloadTask
	if err := download.RetryWith(func(next *DownloadTask) {
		retried = next
		manager.Add(next)
	}); err != nil {
		t.Fatal(err)
	}
	worker.Execute(retried)
	if retried.GetState() != tache.StateSucceeded || retried.GetErr() != nil || client.submissions != 1 || client.genericCalls != 0 || len(client.lookups) != 1 {
		t.Fatalf("receipt retry resubmitted or failed: state=%v, submits=%d, lookups=%d, error=%v", retried.GetState(), client.submissions, len(client.lookups), retried.GetErr())
	}
}

func TestDownload115UnknownSubmissionCannotRetry(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "empty_receipt"},
		{name: "network_timeout", err: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			download, store, client := newDownload115TestTask(t)
			initDownload115TestTask(t, download)
			client.submit = func(context.Context, string, int) (string, error) { return "", test.err }
			download.SetRetry(0, 3)
			worker := tache.Worker[*DownloadTask]{}
			worker.Execute(download)
			if download.Open115SubmissionState() != SubmissionUnknown || download.GetState() != tache.StateFailed || download.Retryable() {
				t.Fatalf("unknown submission was treated as retryable: state=%v, submission=%s", download.GetState(), download.Open115SubmissionState())
			}
			if client.submissions != 1 || store.snapshot().SubmissionState != SubmissionUnknown {
				t.Fatal("unknown cloud result was retried or not saved")
			}
			enqueued := false
			if err := download.RetryWith(func(*DownloadTask) { enqueued = true }); !errors.Is(err, ErrTaskConflict) || enqueued {
				t.Fatalf("manual retry bypassed unknown submission: err=%v, enqueued=%t", err, enqueued)
			}
			if err := download.Run(); !errors.Is(err, ErrTaskConflict) || client.submissions != 1 {
				t.Fatalf("direct rerun bypassed unknown submission: err=%v, submits=%d", err, client.submissions)
			}
		})
	}
}

func TestDownload115AuthoritativeRejectionCanRetry(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	client.submit = func(_ context.Context, _ string, attempt int) (string, error) {
		if attempt == 1 {
			return "", &SubmissionRejectedError{Err: errors.New("request rejected")}
		}
		return "retry-receipt", nil
	}
	download.SetRetry(0, 1)
	worker := tache.Worker[*DownloadTask]{}
	worker.Execute(download)
	if download.GetState() != tache.StateSucceeded || client.submissions != 2 || store.snapshot().InfoHash != "retry-receipt" {
		t.Fatalf("authoritatively rejected request did not retry safely: state=%v, submits=%d, error=%v", download.GetState(), client.submissions, download.GetErr())
	}
}

func TestDownload115MissingCloudRecordKeepsReceiptWithoutPanicking(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	client.lookup = func(context.Context, Open115Reference) (*Open115TaskStatus, error) { return nil, nil }
	worker := tache.Worker[*DownloadTask]{}
	worker.Execute(download)
	if download.GetState() != tache.StateFailed || download.GetErr() == nil || !strings.Contains(download.GetErr().Error(), "record was not found") {
		t.Fatalf("missing cloud record was not handled explicitly: state=%v, error=%v", download.GetState(), download.GetErr())
	}
	if record := store.snapshot(); record.InfoHash != "Cloud-ReCeIpT:+/=" || record.SubmissionState != SubmissionAccepted {
		t.Fatalf("missing cloud record discarded the accepted receipt: %+v", record)
	}
}

func TestDownload115QueuedCancellationPersistsTerminalSnapshot(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	manager.Add(download)
	manager.Cancel(download.GetID())
	snapshot := decodeDownload115Snapshot(t, store.snapshot())
	if snapshot.State != tache.StateCanceled || snapshot.Error != context.Canceled.Error() || snapshot.EndTime == nil {
		t.Fatalf("queued cancellation was not saved as terminal: %+v", snapshot)
	}
	if snapshot.StartTime != nil || client.submissions != 0 {
		t.Fatal("queued cancellation started a cloud task")
	}
}

func TestDownload115FinalErrorIsSavedAfterStateTransition(t *testing.T) {
	download, store, _ := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	download.SetState(tache.StateFailing)
	download.SetState(tache.StateFailed)
	download.SetErr(errors.New("final download failure"))
	snapshot := decodeDownload115Snapshot(t, store.snapshot())
	if snapshot.State != tache.StateFailed || snapshot.Error != "final download failure" || snapshot.EndTime == nil {
		t.Fatalf("terminal error or state was not saved: %+v", snapshot)
	}
}

func TestDownload115LocalDeleteFailureAndLateWorkerSave(t *testing.T) {
	download, store, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	manager.Add(download)
	store.deleteErr = errors.New("cannot write tombstone")
	if err := download.RemoveLocal(); err == nil || !strings.Contains(err.Error(), "cannot write tombstone") {
		t.Fatalf("delete error=%v, want tombstone failure", err)
	}
	if _, exists := manager.GetByID(download.GetID()); !exists || store.snapshot().DeletedAt != nil {
		t.Fatal("failed tombstone write lost the local task")
	}
	store.deleteErr = nil
	if err := download.RemoveLocal(); err != nil {
		t.Fatal(err)
	}
	manager.Remove(download.GetID())
	saves := store.saveCalls
	download.SetProgress(100)
	download.SetState(tache.StateSucceeded)
	download.SetErr(nil)
	if store.snapshot().DeletedAt == nil || store.saveCalls != saves || client.cloudDeletions != 0 {
		t.Fatal("local deletion was revived by a late save or deleted a cloud record")
	}
	if err := download.RemoveLocal(); err != nil || store.deleteCalls != 2 {
		t.Fatalf("local deletion not idempotent: err=%v, delete calls=%d", err, store.deleteCalls)
	}
}

func TestDownload115LearnsPersistedTombstoneFromLateSave(t *testing.T) {
	download, store, _ := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	// Simulate a durable tombstone observed by an older in-memory task object.
	// The store must remain authoritative even before the object learns it.
	if err := store.Delete(download.GetID(), time.Now()); err != nil {
		t.Fatal(err)
	}
	download.SetProgress(100)
	if download.Retryable() || download.GetErr() != nil || store.snapshot().DeletedAt == nil {
		t.Fatalf("late snapshot did not respect durable deletion: retryable=%t, error=%v", download.Retryable(), download.GetErr())
	}
	saves := store.saveCalls
	download.SetState(tache.StateSucceeded)
	if store.saveCalls != saves {
		t.Fatal("task continued writing after learning its tombstone")
	}
}

func TestDownload115ConcurrentManualRetryEnqueuesOnce(t *testing.T) {
	download, _, _ := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	download.SetState(tache.StateFailed)
	manager := tache.NewManager[*DownloadTask](tache.WithRunning(false))
	manager.Add(download)
	const callers = 12
	results := make(chan error, callers)
	start := make(chan struct{})
	var enqueues atomic.Int32
	for range callers {
		go func() {
			<-start
			results <- download.RetryWith(func(next *DownloadTask) {
				enqueues.Add(1)
				manager.Add(next)
			})
		}()
	}
	close(start)
	succeeded := 0
	for range callers {
		err := <-results
		if err == nil {
			succeeded++
		} else if !errors.Is(err, ErrTaskConflict) {
			t.Errorf("retry returned unexpected error: %v", err)
		}
	}
	retried, exists := manager.GetByID(download.GetID())
	if enqueues.Load() != 1 || succeeded != 1 || !exists || retried == download || retried.GetState() != tache.StatePending {
		t.Fatalf("concurrent retries did not enqueue exactly one new execution: enqueues=%d, accepted=%d, exists=%t", enqueues.Load(), succeeded, exists)
	}
	if download.GetState() != tache.StateFailed {
		t.Fatal("manual retry changed the previous execution's terminal state")
	}
}

func TestDownload115ConcurrentTaskInfoReads(t *testing.T) {
	download, _, client := newDownload115TestTask(t)
	initDownload115TestTask(t, download)
	submissionStarted := make(chan struct{})
	allowReceipt := make(chan struct{})
	client.submit = func(ctx context.Context, _ string, _ int) (string, error) {
		close(submissionStarted)
		select {
		case <-allowReceipt:
			return "concurrent-receipt", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	workerDone := make(chan struct{})
	go func() {
		worker := tache.Worker[*DownloadTask]{}
		worker.Execute(download)
		close(workerDone)
	}()
	select {
	case <-submissionStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not reach submission")
	}
	readDone := make(chan error, 1)
	go func() {
		for i := range 300 {
			_ = download.GetID()
			_ = download.GetName()
			_ = download.GetCreator()
			_ = download.GetState()
			_ = download.GetStatus()
			_ = download.GetProgress()
			_ = download.GetStartTime()
			_ = download.GetEndTime()
			_ = download.GetTotalBytes()
			_ = download.GetErr()
			_ = download.Open115Reference()
			_ = download.Open115SubmissionState()
			if _, err := json.Marshal(download); err != nil {
				readDone <- err
				return
			}
			if i == 5 {
				close(allowReceipt)
			}
		}
		readDone <- nil
	}()
	for i := range 100 {
		download.SetProgress(float64(i))
		download.SetStatus("polling")
		download.SetTotalBytes(int64(i))
		download.SetStartTime(time.Now())
		download.SetEndTime(time.Now())
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish after receiving its receipt")
	}
	if download.Open115Reference().InfoHash != "concurrent-receipt" || download.Open115SubmissionState() != SubmissionAccepted {
		t.Fatal("concurrent reads or progress updates lost the cloud receipt")
	}
}

var (
	_ Tool            = (*download115TestTool)(nil)
	_ Open115Tool     = (*download115TestTool)(nil)
	_ offline115Store = (*download115TestStore)(nil)
)
