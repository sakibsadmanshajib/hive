package db

import "sync/atomic"

// ResolveHealth tracks whether the most recent /internal/apikeys/resolve
// attempt reached a real verdict or failed on something infrastructural
// (pool checkout timeout, connection error) rather than a normal key outcome
// (not found, revoked, disabled, expired).
//
// This is not a synthetic probe. It is fed by real traffic on the one
// endpoint edge-api's whole authorization path depends on, so it costs
// nothing beyond an atomic store per resolve call, and a control-plane that
// booted successfully but has served no traffic yet reports healthy by
// default rather than unknown-as-degraded.
//
// See platform/http.RouterConfig.DBReady: a pgxpool.Pool is never nil again
// once opened, even when every checkout is timing out, so `pool != nil`
// alone cannot detect this. This tracker is the runtime half of that signal.
type ResolveHealth struct {
	lastFailed atomic.Bool
}

// NewResolveHealth returns a tracker that reports healthy until a failure is
// recorded.
func NewResolveHealth() *ResolveHealth {
	return &ResolveHealth{}
}

// RecordSuccess marks the most recent resolve attempt as having reached a
// real verdict, whatever that verdict was (found, not found, revoked,
// disabled, expired). All of those mean control-plane could reach the
// database.
func (h *ResolveHealth) RecordSuccess() {
	h.lastFailed.Store(false)
}

// RecordFailure marks the most recent resolve attempt as having failed for
// an infrastructural reason rather than reaching a key verdict.
func (h *ResolveHealth) RecordFailure() {
	h.lastFailed.Store(true)
}

// Degraded reports whether the most recent recorded outcome was a failure.
func (h *ResolveHealth) Degraded() bool {
	return h.lastFailed.Load()
}
