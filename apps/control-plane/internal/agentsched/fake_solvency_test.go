package agentsched

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

// fakeSolvency is the test double for the Solvency seam.
//
// It carries three states rather than a boolean, because the failure this gate
// exists to prevent is the lookup-error one and a stub that can only be rich
// or poor cannot express it at all. A suite built on such a stub passes over
// fail-open entirely: the branch that refuses on an unknown answer is never
// executed, so deleting it stays green (issue #1490).
//
//   - zero value: solvent, the gate lets the caller through
//   - insufficient: the balance is short
//   - lookupErr: the lookup itself failed and the answer is unknown
type fakeSolvency struct {
	mu           sync.Mutex
	insufficient bool
	lookupErr    error

	calls      int
	lastTenant uuid.UUID
	lastFloor  int64
}

func (f *fakeSolvency) Check(ctx context.Context, tenantID uuid.UUID, floorCredits int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastTenant = tenantID
	f.lastFloor = floorCredits
	if f.lookupErr != nil {
		return f.lookupErr
	}
	if f.insufficient {
		return ErrInsufficientCredits
	}
	return nil
}

func (f *fakeSolvency) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// solvent is the default gate for tests whose subject is not the gate.
func solvent() *fakeSolvency { return &fakeSolvency{} }
