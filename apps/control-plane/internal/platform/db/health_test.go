package db_test

import (
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
)

// TestResolveHealth_DefaultsHealthy verifies a freshly constructed tracker
// reports healthy before any traffic has been observed. A control-plane that
// booted seconds ago and has not yet served a resolve call must not report
// degraded for having no data.
func TestResolveHealth_DefaultsHealthy(t *testing.T) {
	h := db.NewResolveHealth()
	if h.Degraded() {
		t.Fatalf("Degraded() = true on a fresh tracker, want false")
	}
}

// TestResolveHealth_FlipsOnFailureThenClearsOnSuccess verifies the tracker
// reflects only the most recent outcome: a single infra-class failure marks
// it degraded, and the next success clears it. This is what lets /health
// react to a pool-contention window without a synthetic probe or extra load —
// it observes the same requests real traffic already drives.
func TestResolveHealth_FlipsOnFailureThenClearsOnSuccess(t *testing.T) {
	h := db.NewResolveHealth()

	h.RecordFailure()
	if !h.Degraded() {
		t.Fatalf("Degraded() = false after RecordFailure, want true")
	}

	h.RecordSuccess()
	if h.Degraded() {
		t.Fatalf("Degraded() = true after RecordSuccess, want false")
	}
}
