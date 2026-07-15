package licensing

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// Source resolves the current Entitlement. Exactly one Source is wired at
// control-plane startup, chosen by deployment mode: FileSource for Hive
// Enterprise (offline signed file, no phone-home) or CloudSource for Hive
// Cloud (sync-path placeholder, see its doc comment). Both return the
// identical Entitlement shape, so the HTTP handler -- and any future caller
// -- never needs to branch on which mode is running.
type Source interface {
	Current(ctx context.Context) (Entitlement, error)
}

// FileSource is the Hive Enterprise seam: an offline signed license file,
// validated locally, matching the NVIDIA Delegated License Server pattern
// (no phone-home). Each Current call re-reads and re-verifies the file --
// cheap enough that no caching lives here. ScheduledSource below adds the
// "validated on a schedule" behavior D9 asks for.
type FileSource struct {
	Path         string
	PublicKeyB64 string
	// Now overrides the clock for tests; nil defaults to time.Now.
	Now func() time.Time
}

// Current reads and verifies the license file at f.Path.
func (f FileSource) Current(_ context.Context) (Entitlement, error) {
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return Entitlement{}, fmt.Errorf("licensing: read license file: %w", err)
	}
	return Verify(data, f.PublicKeyB64, now())
}

// CloudSource is the Hive Cloud seam placeholder. Hive Cloud licenses will
// eventually sync from the billing/subscription system; until that
// integration ships, CloudSource returns a fixed Entitlement so cloud
// deployments are never blocked by an incomplete licensing rollout.
//
// ponytail: placeholder only. Wire to the real billing sync when it ships;
// the Source interface is the seam, nothing downstream needs to change.
type CloudSource struct {
	Entitlement Entitlement
}

// Current returns the configured placeholder Entitlement.
func (c CloudSource) Current(_ context.Context) (Entitlement, error) {
	return c.Entitlement, nil
}

// DefaultCloudEntitlement is what CloudSource returns absent a real sync
// path: a valid, far-future entitlement so the cloud path is functionally
// unrestricted until the billing sync integration lands.
func DefaultCloudEntitlement(now time.Time) Entitlement {
	return Entitlement{
		Tier:        "cloud",
		Seats:       0, // 0 = unmetered pending real sync
		IssuedAt:    now,
		ExpiresAt:   now.AddDate(100, 0, 0),
		ValidatedAt: now,
		Valid:       true,
	}
}

// ScheduledSource wraps any Source and re-validates only every Interval,
// caching the last result (value or error) in between. This is the
// "validated locally on a schedule" requirement from D9, without a
// background goroutine to manage: callers just call Current whenever they
// want, and get the cached result if the interval hasn't elapsed yet.
type ScheduledSource struct {
	Inner    Source
	Interval time.Duration
	// Now overrides the clock for tests; nil defaults to time.Now.
	Now func() time.Time

	mu       sync.Mutex
	lastAt   time.Time
	lastVal  Entitlement
	lastErr  error
	hasValue bool
}

// Current returns the cached result if Interval hasn't elapsed since the
// last underlying call, otherwise re-validates via Inner.
func (s *ScheduledSource) Current(ctx context.Context) (Entitlement, error) {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	nowT := now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hasValue && nowT.Sub(s.lastAt) < s.Interval {
		return s.lastVal, s.lastErr
	}
	val, err := s.Inner.Current(ctx)
	s.lastVal, s.lastErr, s.lastAt, s.hasValue = val, err, nowT, true
	return val, err
}
