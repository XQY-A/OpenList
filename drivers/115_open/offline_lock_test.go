package _115_open

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
)

type observedOfflineLockContext struct {
	context.Context
	once    sync.Once
	waiting chan struct{}
}

func (c *observedOfflineLockContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

type offlineLockOperation struct {
	name     string
	bound    bool
	rejected bool
	call     func(context.Context, *Open115, *atomic.Int32) error
}

func offlineLockOperations() []offlineLockOperation {
	dir := &model.Object{ID: "1", IsFolder: true}
	return []offlineLockOperation{
		{"bound add", true, true, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.AddOfflineTaskForAccount(ctx, "12345", "magnet:?xt=urn:btih:original", dir)
			return err
		}},
		{"add", true, true, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.AddOfflineTask(ctx, "magnet:?xt=urn:btih:original", dir)
			return err
		}},
		{"bound lookup", true, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.GetOfflineTaskForAccount(ctx, "12345", "original")
			return err
		}},
		{"lookup", true, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.GetOfflineTask(ctx, "original")
			return err
		}},
		{"bound delete", true, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			return d.DeleteOfflineTaskForAccount(ctx, "12345", "original")
		}},
		{"delete", false, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			return d.DeleteOfflineTask(ctx, "original", false)
		}},
		{"list", false, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.OfflineList(ctx)
			return err
		}},
		{"bulk add", false, false, func(ctx context.Context, d *Open115, _ *atomic.Int32) error {
			_, err := d.OfflineDownload(ctx, []string{"magnet:?xt=urn:btih:original"}, dir)
			return err
		}},
		{"prepare", true, false, func(ctx context.Context, d *Open115, callbacks *atomic.Int32) error {
			return d.WithOfflineAccount(ctx, "12345", func() error {
				callbacks.Add(1)
				return nil
			})
		}},
	}
}

func awaitOfflineLockWait(t *testing.T, ctx *observedOfflineLockContext, result <-chan error) {
	t.Helper()
	select {
	case <-ctx.waiting:
	case err := <-result:
		t.Fatalf("operation returned before waiting for the held account lock: %v", err)
	case <-time.After(time.Second):
		t.Fatal("operation did not use its context while waiting for the account lock")
	}
}

func awaitOfflineLockResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(time.Second):
		t.Fatal("operation did not finish while the account lock remained held")
		return nil
	}
}

func TestOfflineAccountReadLockCancellation(t *testing.T) {
	for _, operation := range offlineLockOperations() {
		t.Run(operation.name, func(t *testing.T) {
			var requests, callbacks atomic.Int32
			d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { requests.Add(1) })
			if err := d.offlineMu.Lock(context.Background()); err != nil {
				t.Fatal(err)
			}
			locked := true
			t.Cleanup(func() {
				if locked {
					d.offlineMu.Unlock()
				}
			})
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &observedOfflineLockContext{Context: base, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() { result <- operation.call(ctx, d, &callbacks) }()
			awaitOfflineLockWait(t, ctx, result)
			cancel()
			err := awaitOfflineLockResult(t, result)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled wait returned %v", err)
			}
			var rejected *OfflineTaskRejectedError
			if errors.As(err, &rejected) != operation.rejected {
				t.Errorf("wrong pre-dispatch rejection classification: %v", err)
			}
			if requests.Load() != 0 || callbacks.Load() != 0 || d.AccountID() != "12345" {
				t.Fatal("canceled waiter performed work or changed the account")
			}
			d.offlineMu.Unlock()
			locked = false
			if !d.offlineMu.semaphore().TryAcquire(math.MaxInt64) {
				t.Fatal("canceled reader leaked an account lock permit")
			}
			d.offlineMu.Unlock()
		})
	}
}

func TestOfflineAccountReadLockDeadline(t *testing.T) {
	for _, operation := range offlineLockOperations() {
		if operation.name != "bound lookup" && operation.name != "bound delete" {
			continue
		}
		t.Run(operation.name, func(t *testing.T) {
			var requests, callbacks atomic.Int32
			d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { requests.Add(1) })
			if err := d.offlineMu.Lock(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer d.offlineMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- operation.call(ctx, d, &callbacks) }()
			if err := awaitOfflineLockResult(t, result); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expired wait returned %v", err)
			}
			if requests.Load() != 0 {
				t.Fatal("expired waiter sent a cloud request")
			}
		})
	}
}

func TestOfflineAccountBindingRecheckedAfterWait(t *testing.T) {
	for _, operation := range offlineLockOperations() {
		if !operation.bound {
			continue
		}
		for _, uid := range []int64{67890, 0} {
			name := "replaced"
			if uid == 0 {
				name = "dropped"
			}
			t.Run(operation.name+"/"+name, func(t *testing.T) {
				var requests, callbacks atomic.Int32
				d := newOfflineTestDriver(func(http.ResponseWriter, *http.Request) { requests.Add(1) })
				if err := d.offlineMu.Lock(context.Background()); err != nil {
					t.Fatal(err)
				}
				locked := true
				t.Cleanup(func() {
					if locked {
						d.offlineMu.Unlock()
					}
				})
				ctx := &observedOfflineLockContext{Context: context.Background(), waiting: make(chan struct{})}
				result := make(chan error, 1)
				go func() { result <- operation.call(ctx, d, &callbacks) }()
				awaitOfflineLockWait(t, ctx, result)
				// Simulate the account replacement while Init/Drop owns the writer.
				d.accountID.Store(uid)
				d.offlineMu.Unlock()
				locked = false
				if err := awaitOfflineLockResult(t, result); !errors.Is(err, ErrOfflineAccountChanged) {
					t.Fatalf("stale binding after lock wait returned %v", err)
				}
				if requests.Load() != 0 || callbacks.Load() != 0 {
					t.Fatal("stale account waiter performed an operation")
				}
			})
		}
	}
}

func TestOfflineAccountWriterLockCancellation(t *testing.T) {
	for _, action := range []string{"init", "drop"} {
		t.Run(action, func(t *testing.T) {
			d := newOfflineTestDriver(nil)
			if err := d.offlineMu.RLock(context.Background()); err != nil {
				t.Fatal(err)
			}
			defer d.offlineMu.RUnlock()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &observedOfflineLockContext{Context: base, waiting: make(chan struct{})}
			result := make(chan error, 1)
			go func() {
				if action == "init" {
					result <- d.Init(ctx)
				} else {
					result <- d.Drop(ctx)
				}
			}()
			awaitOfflineLockWait(t, ctx, result)
			cancel()
			if err := awaitOfflineLockResult(t, result); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s returned %v", action, err)
			}
			if d.AccountID() != "12345" {
				t.Fatal("canceled account replacement cleared the active binding")
			}
			if !d.offlineMu.semaphore().TryAcquire(1) {
				t.Fatal("canceled writer left subsequent readers blocked")
			}
			d.offlineMu.RUnlock()
		})
	}
}
