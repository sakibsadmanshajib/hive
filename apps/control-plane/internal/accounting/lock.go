package accounting

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// AccountLocker serializes the credit-reservation critical section (balance
// read → policy check → hold post) per account. Without it, concurrent
// inference requests for the same account all observe the same available
// balance and each reserve up to the full amount — a TOCTOU double-spend
// (issue #106).
//
// fn must perform the entire read-check-write critical section. The lock is
// held for the duration of fn and released afterwards, including on error.
type AccountLocker interface {
	WithAccountLock(ctx context.Context, accountID uuid.UUID, fn func(ctx context.Context) error) error
}

// noopAccountLocker performs no serialization. It exists only as an explicit
// opt-out (and to demonstrate the race in tests). Production code must use a
// real locker — NewProcessAccountLocker for single-instance deployments or a
// Postgres advisory locker for multi-instance deployments.
type noopAccountLocker struct{}

func (noopAccountLocker) WithAccountLock(ctx context.Context, _ uuid.UUID, fn func(context.Context) error) error {
	return fn(ctx)
}

// processAccountLocker serializes reservations per account using in-process
// mutexes. It is correct for a single control-plane instance. Multi-instance
// deployments MUST use a cross-process lock (see PgxAccountLocker) because
// in-process mutexes do not coordinate across processes.
type processAccountLocker struct {
	mu    sync.Mutex
	locks map[uuid.UUID]*sync.Mutex
}

// NewProcessAccountLocker returns an in-process per-account locker.
func NewProcessAccountLocker() AccountLocker {
	return &processAccountLocker{locks: make(map[uuid.UUID]*sync.Mutex)}
}

func (l *processAccountLocker) lockFor(accountID uuid.UUID) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	m, ok := l.locks[accountID]
	if !ok {
		m = &sync.Mutex{}
		l.locks[accountID] = m
	}
	return m
}

func (l *processAccountLocker) WithAccountLock(ctx context.Context, accountID uuid.UUID, fn func(context.Context) error) error {
	m := l.lockFor(accountID)
	m.Lock()
	defer m.Unlock()
	return fn(ctx)
}
