package main

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	platformdb "github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
)

// TestDBReadyFuncCombinesBootAndRuntimeSignals pins the composition, not the
// two halves. platform/db already proves ResolveHealth flips and clears, and
// platform/http already proves /health calls DBReady per request. Neither
// notices if this wiring drops one of its two terms: with the runtime term
// deleted here the entire control-plane suite stayed green, which is the same
// shape of unfailable health signal this change exists to remove.
func TestDBReadyFuncCombinesBootAndRuntimeSignals(t *testing.T) {
	// A non-nil pool value is all this needs. It is never dialled: dbReadyFunc
	// only compares it against nil, which is exactly the boot-time condition
	// that a live pool can never falsify again once opened.
	pool := &pgxpool.Pool{}

	t.Run("no pool is never ready", func(t *testing.T) {
		if dbReadyFunc(nil, platformdb.NewResolveHealth())() {
			t.Fatal("dbReadyFunc(nil pool) = true, want false")
		}
	})

	t.Run("pool plus healthy resolve is ready", func(t *testing.T) {
		if !dbReadyFunc(pool, platformdb.NewResolveHealth())() {
			t.Fatal("dbReadyFunc(pool, fresh tracker) = false, want true")
		}
	})

	t.Run("runtime degradation is reported even though the pool is open", func(t *testing.T) {
		rh := platformdb.NewResolveHealth()
		ready := dbReadyFunc(pool, rh)
		if !ready() {
			t.Fatal("ready() before any failure = false, want true")
		}
		rh.RecordFailure()
		if ready() {
			t.Fatal("ready() after a resolve failure = true, want false (same callback, no restart)")
		}
		rh.RecordSuccess()
		if !ready() {
			t.Fatal("ready() after recovery = false, want true")
		}
	})

	t.Run("nil tracker degrades to the boot-time signal", func(t *testing.T) {
		if !dbReadyFunc(pool, nil)() {
			t.Fatal("dbReadyFunc(pool, nil tracker) = false, want true")
		}
	})
}
