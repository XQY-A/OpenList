package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/task"
	"github.com/OpenListTeam/tache"
	log "github.com/sirupsen/logrus"
)

const (
	SubmissionPending    = "pending"
	SubmissionSubmitting = "submitting"
	SubmissionAccepted   = "accepted"
	SubmissionFailed     = "failed"
	SubmissionUnknown    = "unknown"
	SubmissionLegacy     = "legacy"
	open115RecordVersion = 1
	open115Timeout       = 30 * time.Second
)

// Only the fields needed to display and resume this task are stored. In
// particular, neither driver credentials nor a serialized user belong here.
type download115Snapshot struct {
	URL          string       `json:"url"`
	DstDirPath   string       `json:"dst_dir_path"`
	TempDir      string       `json:"temp_dir"`
	DeletePolicy DeletePolicy `json:"delete_policy"`
	APIURL       string       `json:"api_url,omitempty"`
	State        tache.State  `json:"state"`
	Status       string       `json:"status"`
	Progress     float64      `json:"progress"`
	TotalBytes   int64        `json:"total_bytes"`
	Error        string       `json:"error,omitempty"`
	StartTime    *time.Time   `json:"start_time,omitempty"`
	EndTime      *time.Time   `json:"end_time,omitempty"`
}

type offline115Store interface {
	Create(*model.OfflineDownload115Task) error
	Save(*model.OfflineDownload115Task) error
	Delete(string, time.Time) error
}

type database115Store struct{}

func (database115Store) Create(record *model.OfflineDownload115Task) error {
	return db.CreateOfflineDownload115Task(record)
}
func (database115Store) Save(record *model.OfflineDownload115Task) error {
	return db.SaveOfflineDownload115Task(record)
}
func (database115Store) Delete(id string, at time.Time) error {
	return db.MarkOfflineDownload115TaskDeleted(id, at)
}

func (t *DownloadTask) Is115Open() bool { return t.Toolname == Open115Name }

func (t *DownloadTask) store115() offline115Store {
	if t.open115Store != nil {
		return t.open115Store
	}
	return database115Store{}
}

func (t *DownloadTask) snapshot115Locked() download115Snapshot {
	snapshot := download115Snapshot{
		URL: t.Url, DstDirPath: t.DstDirPath, TempDir: t.TempDir,
		DeletePolicy: t.DeletePolicy, APIURL: t.ApiUrl,
		State: t.Base.GetState(), Status: t.Status,
		Progress: t.Base.GetProgress(), TotalBytes: t.TotalBytes,
		StartTime: copyTime(t.StartTime), EndTime: copyTime(t.EndTime),
	}
	if err := t.Base.GetErr(); err != nil {
		snapshot.Error = err.Error()
	}
	if snapshot.State == tache.StateCanceling {
		snapshot.State = tache.StateCanceled
		snapshot.Error = context.Canceled.Error()
		if snapshot.EndTime == nil {
			now := time.Now()
			snapshot.EndTime = &now
		}
	}
	return snapshot
}

func (t *DownloadTask) init115(id string) error {
	t.SetID(id)
	now := time.Now()
	t.mu.Lock()
	t.open115 = &model.OfflineDownload115Task{
		TaskID: id, Version: open115RecordVersion,
		SubmissionState: SubmissionPending, CloudDeleteState: "none",
		CreatedAt: now, UpdatedAt: now,
	}
	if t.Creator != nil {
		t.open115.CreatorID = t.Creator.ID
	}
	snapshot, err := json.Marshal(t.snapshot115Locked())
	if err == nil {
		t.open115.Snapshot = string(snapshot)
		err = t.store115().Create(t.open115)
	}
	t.mu.Unlock()
	if err != nil {
		return fmt.Errorf("failed to persist 115 Open task before enqueueing: %w", err)
	}
	return nil
}

func (t *DownloadTask) save115() error {
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	return t.save115Locked()
}

// The persistence lock must be taken before copying state. Otherwise a stale
// progress snapshot could overwrite a newly accepted hash or a delete marker.
func (t *DownloadTask) save115Locked() error {
	t.mu.Lock()
	if t.retired || t.open115 == nil || t.open115.DeletedAt != nil {
		t.mu.Unlock()
		return nil
	}
	record := *t.open115
	snapshot, err := json.Marshal(t.snapshot115Locked())
	if err == nil {
		record.Snapshot = string(snapshot)
		record.UpdatedAt = time.Now()
	}
	t.mu.Unlock()
	if err == nil {
		err = t.store115().Save(&record)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if errors.Is(err, db.ErrOfflineDownload115TaskDeleted) {
		// An in-flight worker must never resurrect a locally removed task.
		now := time.Now()
		t.open115.DeletedAt = &now
		err = nil
	}
	if err != nil {
		t.persistErr = fmt.Errorf("failed to persist 115 Open task: %w", err)
		log.Errorf("115 Open task %s persistence failed: %v", record.TaskID, err)
		return t.persistErr
	}
	t.persistErr = nil
	t.open115.Snapshot = record.Snapshot
	t.open115.UpdatedAt = record.UpdatedAt
	return nil
}

func (t *DownloadTask) Open115Reference() Open115Reference {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.open115 == nil {
		return Open115Reference{}
	}
	return Open115Reference{t.open115.StorageID, t.open115.AccountID, t.open115.InfoHash}
}

func (t *DownloadTask) Open115SubmissionState() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.open115 == nil {
		return SubmissionLegacy
	}
	return t.open115.SubmissionState
}

// Local removal does not cancel an already queued or running download. Its
// current execution may finish, while persistence and retries honor the
// tombstone and cannot restore the removed list entry.
func (t *DownloadTask) run115AllowedLocked() error {
	if t.retired {
		return fmt.Errorf("%w: the task has already been retried", ErrTaskConflict)
	}
	if t.Creator == nil || t.Creator.ID == 0 {
		return fmt.Errorf("%w: the task creator is unavailable", ErrTaskConflict)
	}
	if t.open115 == nil || t.open115.SubmissionState == SubmissionLegacy {
		return fmt.Errorf("%w: historical 115 Open task has no reliable cloud reference", ErrTaskConflict)
	}
	if t.open115.CloudDeleteState != "none" {
		return fmt.Errorf("%w: 115 Open task is being deleted", ErrTaskConflict)
	}
	if t.open115.SubmissionState == SubmissionUnknown || t.open115.SubmissionState == SubmissionSubmitting {
		return fmt.Errorf("%w: 115 Open submission result is unknown; verify the cloud task before creating a new download", ErrTaskConflict)
	}
	return nil
}

func (t *DownloadTask) retry115AllowedLocked() error {
	if err := t.run115AllowedLocked(); err != nil {
		return err
	}
	if t.open115.DeletedAt != nil {
		return fmt.Errorf("%w: 115 Open task was removed", ErrTaskConflict)
	}
	return nil
}

func (t *DownloadTask) Retryable() bool {
	if !t.Is115Open() {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.retry115AllowedLocked() == nil
}

func (t *DownloadTask) lockOperation() func() {
	t.mu.Lock()
	if t.operationMu == nil {
		t.operationMu = &sync.Mutex{}
	}
	lock := t.operationMu
	t.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// RetryWith replaces a finished 115 execution with a new object using the same
// durable task ID. Reusing tache.Manager.Retry on the old object lets its worker
// observe WaitingRetry while another worker dequeues the very same task.
func (t *DownloadTask) RetryWith(enqueue func(*DownloadTask)) error {
	unlock := t.lockOperation()
	defer unlock()
	if !t.Is115Open() {
		enqueue(t)
		return nil
	}
	t.persistMu.Lock()
	t.mu.RLock()
	err := t.retry115AllowedLocked()
	terminal := isDownloadTerminal(t.Base.GetState())
	if err == nil && !terminal {
		err = fmt.Errorf("%w: only finished 115 Open tasks can be retried", ErrTaskConflict)
	}
	if err == nil && t.Base.GetState() == tache.StateCanceled && !conf.Conf.Tasks.AllowRetryCanceled {
		err = fmt.Errorf("%w: retrying canceled tasks is disabled", ErrTaskConflict)
	}
	if err != nil {
		t.mu.RUnlock()
		t.persistMu.Unlock()
		return err
	}
	record := *t.open115
	next := &DownloadTask{
		TaskExtension: task.TaskExtension{
			Base:    tache.Base{ID: t.ID, State: tache.StatePending, MaxRetry: t.MaxRetry},
			Creator: t.Creator, ApiUrl: t.ApiUrl,
		},
		Url: t.Url, DstDirPath: t.DstDirPath, TempDir: t.TempDir,
		DeletePolicy: t.DeletePolicy, Toolname: t.Toolname, tool: t.tool,
		GID: record.InfoHash, open115: &record, open115Store: t.open115Store,
		operationMu: t.operationMu,
	}
	t.mu.RUnlock()
	if err := next.save115(); err != nil {
		t.persistMu.Unlock()
		return err
	}
	if next.open115.DeletedAt != nil {
		t.mu.Lock()
		t.open115.DeletedAt = copyTime(next.open115.DeletedAt)
		t.mu.Unlock()
		t.persistMu.Unlock()
		return fmt.Errorf("%w: 115 Open task was removed", ErrTaskConflict)
	}
	t.mu.Lock()
	t.retired = true
	t.mu.Unlock()
	t.persistMu.Unlock()
	enqueue(next)
	return nil
}

// RemoveLocal is synchronous even when general task persistence is disabled.
// The manager must only remove its entry after this method succeeds.
func (t *DownloadTask) RemoveLocal() error {
	unlock := t.lockOperation()
	defer unlock()
	return t.removeLocal115()
}

func (t *DownloadTask) removeLocal115() error {
	if !t.Is115Open() {
		return nil
	}
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.RLock()
	if t.retired {
		t.mu.RUnlock()
		return fmt.Errorf("%w: the task has already been retried", ErrTaskConflict)
	}
	if t.open115 == nil {
		t.mu.RUnlock()
		return errors.New("115 Open task is missing its durable record")
	}
	deleted := t.open115.DeletedAt != nil
	id := t.open115.TaskID
	t.mu.RUnlock()
	if deleted {
		return nil
	}
	now := time.Now()
	if err := t.store115().Delete(id, now); err != nil {
		return fmt.Errorf("failed to persist local task deletion: %w", err)
	}
	t.mu.Lock()
	t.open115.DeletedAt = &now
	t.mu.Unlock()
	return nil
}

func (t *DownloadTask) durable115Tool() (Open115Tool, error) {
	candidate, err := t.resolveTool()
	if err != nil {
		return nil, err
	}
	client, ok := candidate.(Open115Tool)
	if !ok {
		return nil, errors.New("115 Open tool does not support durable downloads")
	}
	return client, nil
}

func (t *DownloadTask) set115Submission(state, hash string, reference Open115Reference) error {
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.Lock()
	if t.retired || t.open115 == nil {
		t.mu.Unlock()
		return fmt.Errorf("%w: 115 Open task was removed", ErrTaskConflict)
	}
	t.open115.SubmissionState = state
	if reference.StorageID != 0 {
		t.open115.StorageID = reference.StorageID
		t.open115.AccountID = reference.AccountID
	}
	if hash != "" {
		t.open115.InfoHash = hash
		t.GID = hash
		now := time.Now()
		t.open115.AcceptedAt = &now
	}
	t.mu.Unlock()
	return t.save115Locked()
}

func (t *DownloadTask) run115() error {
	client, err := t.durable115Tool()
	if err != nil {
		return err
	}
	t.mu.RLock()
	err = t.run115AllowedLocked()
	t.mu.RUnlock()
	if err != nil {
		return err
	}
	// A known receipt survives a failed database write in memory. Persist it
	// before doing anything else on retry, without submitting the URL again.
	if err := t.save115(); err != nil {
		return err
	}
	reference := t.Open115Reference()
	if reference.InfoHash == "" {
		ctx, cancel := context.WithTimeout(t.Ctx(), open115Timeout)
		prepared, prepareErr := client.Prepare(ctx, t.TempDir)
		cancel()
		if prepareErr != nil {
			_ = t.set115Submission(SubmissionFailed, "", Open115Reference{})
			return prepareErr
		}
		if prepared == nil || prepared.Reference.StorageID == 0 || prepared.Reference.AccountID == "" || prepared.Submit == nil {
			_ = t.set115Submission(SubmissionFailed, "", Open115Reference{})
			return errors.New("115 Open preparation returned an incomplete storage binding")
		}
		unlock := lock115Account(prepared.Reference.AccountID)
		err = t.set115Submission(SubmissionSubmitting, "", prepared.Reference)
		if err != nil {
			// No cloud request was sent. A local retry may attempt the write
			// again; a crash with a submitting row remains conservative.
			t.mu.Lock()
			if t.open115 != nil {
				t.open115.SubmissionState = SubmissionFailed
			}
			t.mu.Unlock()
			unlock()
			return err
		}
		ctx, cancel = context.WithTimeout(t.Ctx(), open115Timeout)
		hash, submitErr := prepared.Submit(ctx, t.Url)
		cancel()
		if hash != "" {
			err = t.set115Submission(SubmissionAccepted, hash, prepared.Reference)
			unlock()
			if err != nil {
				return fmt.Errorf("115 accepted the download, but its receipt could not be saved: %w", err)
			}
		} else {
			state := SubmissionUnknown
			var rejected *SubmissionRejectedError
			if errors.As(submitErr, &rejected) {
				state = SubmissionFailed
			}
			if submitErr == nil {
				submitErr = errors.New("115 returned an empty download receipt")
			}
			saveErr := t.set115Submission(state, "", prepared.Reference)
			unlock()
			return errors.Join(submitErr, saveErr)
		}
		reference = t.Open115Reference()
	}
	if reference.StorageID == 0 || reference.AccountID == "" {
		return errors.New("115 Open task has no reliable storage and account binding")
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	statusFailures := 0
	for {
		if t.Ctx().Err() != nil {
			// Cancellation retains the original record-only behavior. This
			// independent context is bounded because the task's is canceled.
			ctx, cancel := context.WithTimeout(context.Background(), open115Timeout)
			unlock := lock115Account(reference.AccountID)
			err := client.Delete(ctx, reference)
			unlock()
			cancel()
			return errors.Join(t.Ctx().Err(), err)
		}
		ctx, cancel := context.WithTimeout(t.Ctx(), open115Timeout)
		unlock := lock115Account(reference.AccountID)
		status, err := client.Lookup(ctx, reference)
		unlock()
		cancel()
		if err != nil {
			statusFailures++
			if statusFailures > 5 {
				return fmt.Errorf("failed to query 115 Open download after %d attempts: %w", statusFailures, err)
			}
		} else if status == nil {
			return errors.New("115 Open download record was not found")
		} else {
			statusFailures = 0
			t.mu.Lock()
			t.Base.SetProgress(status.Progress)
			t.TotalBytes = status.TotalBytes
			t.Status = fmt.Sprintf("[%s]: %s", Open115Name, status.Status.Status)
			t.mu.Unlock()
			if err := t.save115(); err != nil {
				return err
			}
			if status.Err != nil {
				return status.Err
			}
			if status.Completed {
				return t.Transfer()
			}
		}
		select {
		case <-t.CtxDone():
		case <-ticker.C:
		}
	}
}
