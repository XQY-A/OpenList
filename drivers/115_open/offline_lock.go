package _115_open

import (
	"context"
	"math"
	"sync"

	"golang.org/x/sync/semaphore"
)

// offlineAccountMutex allows concurrent offline requests, while Init and Drop
// exclude them during account replacement. Its zero value is ready for use.
// Weighted acquisition queues writers ahead of later readers, and cancellation
// removes a waiter without leaving a goroutine that could acquire the lock later.
type offlineAccountMutex struct {
	once sync.Once
	sem  *semaphore.Weighted
}

func (m *offlineAccountMutex) semaphore() *semaphore.Weighted {
	m.once.Do(func() { m.sem = semaphore.NewWeighted(math.MaxInt64) })
	return m.sem
}

func (m *offlineAccountMutex) Lock(ctx context.Context) error {
	return m.semaphore().Acquire(ctx, math.MaxInt64)
}

func (m *offlineAccountMutex) Unlock() {
	m.semaphore().Release(math.MaxInt64)
}

func (m *offlineAccountMutex) RLock(ctx context.Context) error {
	return m.semaphore().Acquire(ctx, 1)
}

func (m *offlineAccountMutex) RUnlock() {
	m.semaphore().Release(1)
}
