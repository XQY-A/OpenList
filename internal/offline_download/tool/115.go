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

// Open115ReferenceError supplies a stable reason without exposing the saved
// account identity. Its cause distinguishes a changed binding from a database
// failure, so callers can preserve the appropriate response code.
type Open115ReferenceError struct {
	Reason string
	Err    error
}

func (e *Open115ReferenceError) Error() string { return e.Err.Error() }
func (e *Open115ReferenceError) Unwrap() error { return e.Err }

// contextMutex is shared by ordinary task operations and bounded cleanup
// requests. A canceled waiter never needs a goroutine to acquire and release
// a mutex after its caller has already returned.
type contextMutex struct {
	once sync.Once
	gate chan struct{}
}

func (m *contextMutex) Lock(ctx context.Context) error {
	m.once.Do(func() { m.gate = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *contextMutex) Unlock() { <-m.gate }

var open115Accounts = struct {
	sync.Mutex
	locks map[string]*accountLock
}{locks: make(map[string]*accountLock)}

type accountLock struct {
	contextMutex
	users int
}

// Serializing by account also covers submissions whose hash is not known yet.
// This coordinates this process only; the 115 API has no conditional deletion
// primitive with which to exclude another client's concurrent submission.
func lock115Account(accountID string) func() {
	unlock, _ := lock115AccountContext(context.Background(), accountID)
	return unlock
}

func lock115AccountContext(ctx context.Context, accountID string) (func(), error) {
	open115Accounts.Lock()
	lock := open115Accounts.locks[accountID]
	if lock == nil {
		lock = &accountLock{}
		open115Accounts.locks[accountID] = lock
	}
	lock.users++
	open115Accounts.Unlock()
	release := func() {
		open115Accounts.Lock()
		lock.users--
		if lock.users == 0 {
			delete(open115Accounts.locks, accountID)
		}
		open115Accounts.Unlock()
	}
	if err := lock.Lock(ctx); err != nil {
		release()
		return nil, err
	}
	return func() {
		lock.Unlock()
		release()
	}, nil
}
