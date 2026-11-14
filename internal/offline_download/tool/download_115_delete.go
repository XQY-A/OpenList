package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/db"
)

const (
	CloudDeleteNone     = "none"
	CloudDeleteDeleting = "deleting"
	CloudDeleteUnknown  = "unknown"
	CloudDeleteDeleted  = "deleted"
)

// CloudDeleteResult separates a confirmed cloud outcome from local cleanup.
// The cloud record may already be deleted even if saving that fact or the
// local tombstone fails. Callers must retain the manager entry on any error.
type CloudDeleteResult struct {
	CloudState   string `json:"cloud_state"`
	LocalDeleted bool   `json:"local_deleted"`
}

// CloudDeleteError carries a stable reason for API clients while preserving
// the underlying conflict, cancellation or persistence error.
type CloudDeleteError struct {
	Reason string
	Err    error
}

func (e *CloudDeleteError) Error() string { return e.Err.Error() }
func (e *CloudDeleteError) Unwrap() error { return e.Err }

func cloudDeleteConflict(reason, message string) *CloudDeleteError {
	return &CloudDeleteError{Reason: reason, Err: fmt.Errorf("%w: %s", ErrTaskConflict, message)}
}

func cloudDeleteReferenceError(err error) *CloudDeleteError {
	reason := "storage_unavailable"
	var referenceErr *Open115ReferenceError
	if errors.As(err, &referenceErr) {
		reason = referenceErr.Reason
	}
	return &CloudDeleteError{Reason: reason, Err: err}
}

func cloudDeleteRequestError(reason string, err error) *CloudDeleteError {
	var referenceErr *Open115ReferenceError
	if errors.As(err, &referenceErr) {
		return cloudDeleteReferenceError(err)
	}
	return &CloudDeleteError{Reason: reason, Err: err}
}

func (t *DownloadTask) cloudDeleteStateLocked() string {
	if t.open115 == nil || t.open115.CloudDeleteState == "" {
		return CloudDeleteNone
	}
	return t.open115.CloudDeleteState
}

// Only task-local checks belong under mu. Driver lookup and binding validation
// run after releasing it, because resolving the tool can acquire the same lock.
func (t *DownloadTask) cloudDeleteAllowedLocked() *CloudDeleteError {
	if !t.Is115Open() {
		return cloudDeleteConflict("unsupported_tool", "only 115 Open tasks support cloud record deletion")
	}
	if t.retired {
		return cloudDeleteConflict("task_retried", "the task has already been retried")
	}
	if t.open115 != nil && t.open115.DeletedAt != nil {
		return cloudDeleteConflict("task_removed", "the local task has already been removed")
	}
	if !isDownloadTerminal(t.Base.GetState()) {
		return cloudDeleteConflict("task_not_finished", "only finished tasks support cloud record deletion")
	}
	if t.open115 == nil || t.open115.SubmissionState == SubmissionLegacy {
		return cloudDeleteConflict("legacy_task", "historical 115 Open task has no reliable cloud reference")
	}
	if t.open115.SubmissionState != SubmissionAccepted || t.open115.InfoHash == "" || t.open115.StorageID == 0 || t.open115.AccountID == "" {
		return cloudDeleteConflict("cloud_reference_missing", "115 Open task has no reliable cloud receipt and account binding")
	}
	switch t.cloudDeleteStateLocked() {
	case CloudDeleteNone, CloudDeleteDeleting, CloudDeleteUnknown, CloudDeleteDeleted:
	default:
		return cloudDeleteConflict("cleanup_state_invalid", "115 Open task has an unsupported cleanup state")
	}
	return nil
}

func (t *DownloadTask) cloudDeleteResult() CloudDeleteResult {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return CloudDeleteResult{CloudState: t.cloudDeleteStateLocked()}
}

// setCloudDeleteState publishes an intent or its rollback only after saving
// it. Failed confirmation writes must instead preserve the cloud evidence in
// memory. The caller holds operation and account locks.
func (t *DownloadTask) setCloudDeleteState(state string) error {
	t.persistMu.Lock()
	defer t.persistMu.Unlock()
	t.mu.Lock()
	if err := t.cloudDeleteAllowedLocked(); err != nil {
		t.mu.Unlock()
		return err
	}
	record := *t.open115
	record.CloudDeleteState = state
	if state == CloudDeleteDeleted && record.CloudDeletedAt == nil {
		now := time.Now()
		record.CloudDeletedAt = &now
	}
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
		// A different instance may have removed this task during Lookup.
		// Unlike ordinary late snapshots, deletion cannot absorb this error
		// and then treat it as permission to dispatch a cloud mutation.
		now := time.Now()
		t.open115.DeletedAt = &now
		t.persistErr = nil
		return t.cloudDeleteAllowedLocked()
	}
	if err != nil {
		if state == CloudDeleteDeleted || state == CloudDeleteUnknown {
			t.open115.CloudDeleteState = state
			t.open115.CloudDeletedAt = record.CloudDeletedAt
		}
		t.persistErr = fmt.Errorf("failed to persist 115 Open task: %w", err)
		return &CloudDeleteError{Reason: "persistence_failed", Err: t.persistErr}
	}
	t.open115.CloudDeleteState = state
	t.open115.CloudDeletedAt = record.CloudDeletedAt
	t.open115.Snapshot = record.Snapshot
	t.open115.UpdatedAt = record.UpdatedAt
	t.persistErr = nil
	if err := t.cloudDeleteAllowedLocked(); err != nil {
		return err
	}
	return nil
}

// DeleteCloudRecord deletes only the saved 115 download record, never its
// files. Permissions are checked by the caller, which supplies a task ID rather
// than accepting a cloud hash or account identity from the request.
func (t *DownloadTask) DeleteCloudRecord(ctx context.Context) (CloudDeleteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, open115Timeout)
	defer cancel()
	unlock, err := t.lockOperationContext(ctx)
	if err != nil {
		return t.cloudDeleteResult(), &CloudDeleteError{Reason: "operation_canceled", Err: err}
	}
	defer unlock()

	t.mu.RLock()
	allowedErr := t.cloudDeleteAllowedLocked()
	t.mu.RUnlock()
	if allowedErr != nil {
		return t.cloudDeleteResult(), allowedErr
	}
	client, err := t.durable115Tool()
	if err != nil {
		return t.cloudDeleteResult(), cloudDeleteReferenceError(err)
	}
	reference := t.Open115Reference()
	unlockAccount, err := lock115AccountContext(ctx, reference.AccountID)
	if err != nil {
		return t.cloudDeleteResult(), &CloudDeleteError{Reason: "operation_canceled", Err: err}
	}
	defer unlockAccount()
	t.mu.RLock()
	allowedErr = t.cloudDeleteAllowedLocked()
	t.mu.RUnlock()
	if allowedErr != nil {
		return t.cloudDeleteResult(), allowedErr
	}
	if err := client.ValidateReference(reference); err != nil {
		return t.cloudDeleteResult(), cloudDeleteReferenceError(err)
	}
	if err := ctx.Err(); err != nil {
		return t.cloudDeleteResult(), &CloudDeleteError{Reason: "operation_canceled", Err: err}
	}
	initialState := t.cloudDeleteResult().CloudState
	firstAttempt := initialState == CloudDeleteNone
	if firstAttempt {
		// Even a reliable absence is evidence about one point in time. Save
		// the intent before looking it up, so a crash or failed confirmation
		// write cannot authorize deletion of a later record with this hash.
		if err := t.setCloudDeleteState(CloudDeleteDeleting); err != nil {
			return t.cloudDeleteResult(), err
		}
	}
	beforeDispatchError := func(cause *CloudDeleteError) (CloudDeleteResult, error) {
		// This invocation has neither observed absence nor dispatched a
		// mutation. A durable rollback lets a later request try normally.
		// Failure keeps the intent and therefore requires reconciliation.
		if firstAttempt {
			if err := t.setCloudDeleteState(CloudDeleteNone); err != nil {
				return t.cloudDeleteResult(), err
			}
		}
		return t.cloudDeleteResult(), cause
	}
	status, err := client.Lookup(ctx, reference)
	if err != nil {
		return beforeDispatchError(cloudDeleteRequestError("cloud_query_failed", err))
	}
	if status != nil {
		// A prior successful deletion is durable evidence about the old
		// record. The same hash appearing again may belong to a new download.
		if initialState == CloudDeleteDeleted {
			return t.cloudDeleteResult(), cloudDeleteConflict("cloud_record_reappeared", "the previously deleted cloud record has reappeared; refusing to delete a new download")
		}
		if !firstAttempt {
			return t.cloudDeleteResult(), cloudDeleteConflict("cloud_delete_unknown", "the previous cloud deletion outcome is unknown; refusing to delete a record that may belong to a new download")
		}
		switch status.State {
		case 2, -1:
		case 0, 1:
			return beforeDispatchError(cloudDeleteConflict("cloud_task_active", "the cloud download is still active"))
		default:
			return beforeDispatchError(cloudDeleteConflict("cloud_state_unknown", "the cloud download state is not recognized"))
		}
		// Recheck the durable row after Lookup, since another process may
		// have written a local tombstone while this request was querying.
		if err := t.setCloudDeleteState(CloudDeleteDeleting); err != nil {
			return t.cloudDeleteResult(), err
		}
		if err := ctx.Err(); err != nil {
			return beforeDispatchError(&CloudDeleteError{Reason: "operation_canceled", Err: err})
		}
		if err := client.Delete(ctx, reference); err != nil {
			// A failed response does not prove whether 115 applied the delete.
			// Later attempts may only reconcile absence, never delete again.
			saveErr := t.setCloudDeleteState(CloudDeleteUnknown)
			return t.cloudDeleteResult(), cloudDeleteRequestError("cloud_delete_unknown", errors.Join(err, saveErr))
		}
	}
	// Complete these synchronous writes even if the request was canceled just
	// after cloud confirmation. Losing that evidence would make retry unsafe.
	if err := t.setCloudDeleteState(CloudDeleteDeleted); err != nil {
		return t.cloudDeleteResult(), err
	}
	if err := t.removeLocal115(); err != nil {
		return t.cloudDeleteResult(), &CloudDeleteError{Reason: "persistence_failed", Err: err}
	}
	result := t.cloudDeleteResult()
	result.LocalDeleted = true
	return result, nil
}
