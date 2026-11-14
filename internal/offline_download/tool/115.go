package tool

import (
	"context"
	"errors"
	"sync"
)

const Open115Name = "115 Open"

// Open115Reference identifies the actual storage and account that accepted a
// download. A mount path is not an identity: it can be renamed or reused.
type Open115Reference struct {
	StorageID uint
	AccountID string
	InfoHash  string
}

type Open115Submission struct {
	Reference Open115Reference
	Submit    func(context.Context, string) (string, error)
}

type Open115TaskStatus struct {
	Status
	State int
}

// Open115Tool separates preparation from submission so the binding and the
// submitting state can be committed before sending a cloud download request.
type Open115Tool interface {
	Prepare(context.Context, string) (*Open115Submission, error)
	Lookup(context.Context, Open115Reference) (*Open115TaskStatus, error)
	Delete(context.Context, Open115Reference) error
	ValidateReference(Open115Reference) error
}

// SubmissionRejectedError is reserved for authoritative rejection or failure
// before dispatch. All other submission errors are treated as unknown results.
type SubmissionRejectedError struct{ Err error }

func (e *SubmissionRejectedError) Error() string { return e.Err.Error() }
func (e *SubmissionRejectedError) Unwrap() error { return e.Err }

var ErrTaskConflict = errors.New("task operation is not available")

var open115Accounts = struct {
	sync.Mutex
	locks map[string]*accountLock
}{locks: make(map[string]*accountLock)}

type accountLock struct {
	sync.Mutex
	users int
}

// Serializing by account also covers submissions whose hash is not known yet.
// This coordinates this process only; the 115 API has no conditional deletion
// primitive with which to exclude another client's concurrent submission.
func lock115Account(accountID string) func() {
	open115Accounts.Lock()
	lock := open115Accounts.locks[accountID]
	if lock == nil {
		lock = &accountLock{}
		open115Accounts.locks[accountID] = lock
	}
	lock.users++
	open115Accounts.Unlock()
	lock.Lock()
	return func() {
		lock.Unlock()
		open115Accounts.Lock()
		lock.users--
		if lock.users == 0 {
			delete(open115Accounts.locks, accountID)
		}
		open115Accounts.Unlock()
	}
}
