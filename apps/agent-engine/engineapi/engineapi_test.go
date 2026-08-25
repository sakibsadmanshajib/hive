package engineapi_test

// These tests exercise the SANCTIONED PUBLIC DOOR (engineapi.go's own package
// doc) into the sandbox engine, without ever launching a real Apptainer
// sandbox: every case here returns from engineapi.Config.ResolveEgressHosts
// before Launch reaches sandbox.BuildArgv or os/exec, which is the earliest
// point Launch touches anything this WSL2 box cannot run. What IS exercised
// for real: quota wiring and its documented defaults (issue #305/#308), the
// Task.Pack precondition, unknown-session error mapping, and that a resolver
// failure propagates as a specific, wrapped error rather than a silent
// success or a generic one.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/agent-engine/engineapi"
	"github.com/sakibsadmanshajib/hive/apps/agent-engine/internal/quota"
)

// blockingResolver returns a ResolveEgressHosts func that signals on entered
// the moment it is called, then waits for release before returning stopErr.
// Because Launch calls ResolveEgressHosts strictly after acquiring the quota
// slot and strictly before touching the filesystem or exec, this lets a test
// hold a quota slot open for as long as it wants without ever starting a
// sandbox.
func blockingResolver(entered chan struct{}, release <-chan struct{}, stopErr error) func(ctx context.Context, tenantID, userID uuid.UUID) ([]string, error) {
	return func(_ context.Context, _, _ uuid.UUID) ([]string, error) {
		entered <- struct{}{}
		<-release
		return nil, stopErr
	}
}

// TestLaunch_RequiresPack would catch a regression where Launch stopped
// validating Task.Pack and instead fell through to acquiring a quota slot
// (or worse, attempted to launch) for a task with no pack name at all.
func TestLaunch_RequiresPack(t *testing.T) {
	eng := engineapi.New(engineapi.Config{
		ResolveEgressHosts: func(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
			t.Fatal("ResolveEgressHosts must not be called before the Task.Pack precondition is checked")
			return nil, nil
		},
	})
	ref, err := eng.Launch(context.Background(), engineapi.Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New()})
	if err == nil {
		t.Fatal("expected an error for a Task with no Pack")
	}
	if !strings.Contains(err.Error(), "Pack is required") {
		t.Fatalf("expected a Pack-is-required error, got: %v", err)
	}
	if ref != "" {
		t.Fatalf("expected an empty session ref on error, got %q", ref)
	}
}

// TestLaunch_EgressPolicyErrorPropagates would catch a regression where
// Launch swallowed a resolver failure and returned a generic error (or,
// worse, a fabricated success) instead of the specific, wrapped cause.
func TestLaunch_EgressPolicyErrorPropagates(t *testing.T) {
	sentinel := errors.New("egress policy backend unreachable")
	eng := engineapi.New(engineapi.Config{
		ResolveEgressHosts: func(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
			return nil, sentinel
		},
	})
	ref, err := eng.Launch(context.Background(), engineapi.Task{ID: uuid.New(), TenantID: uuid.New(), UserID: uuid.New(), Pack: "coding-pack"})
	if err == nil {
		t.Fatal("expected Launch to fail when the egress resolver fails")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the returned error to wrap the resolver's sentinel error, got: %v", err)
	}
	if ref != "" {
		t.Fatalf("expected an empty session ref on error, got %q", ref)
	}
}

// TestLaunch_TenantQuotaEnforcedUnderConcurrency would catch a regression in
// either direction: Config.QuotaTenantConcurrency silently not being wired
// into the quota manager (a resource-exhaustion vector — the whole point of
// issue #305/#308), or the documented default of 4 (CLAUDE.md, engine.go's
// withDefaults) silently changing. Each case holds exactly `cap` concurrent
// Launch calls open (parked inside ResolveEgressHosts, past the quota
// Acquire but before anything a sandbox would need) and proves a same-tenant
// attempt made while all `cap` slots are held is rejected specifically with
// quota.ErrTenantQuotaExceeded — never accepted, never a different error.
func TestLaunch_TenantQuotaEnforcedUnderConcurrency(t *testing.T) {
	cases := []struct {
		name      string
		configCap int // 0 means "leave unset", exercising the documented default
		wantCap   int
	}{
		{name: "explicit cap of 1", configCap: 1, wantCap: 1},
		{name: "explicit cap of 3", configCap: 3, wantCap: 3},
		{name: "unset cap falls back to documented default of 4", configCap: 0, wantCap: 4},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entered := make(chan struct{}, c.wantCap)
			release := make(chan struct{})
			stopErr := errors.New("deliberate stop before any sandbox work")

			eng := engineapi.New(engineapi.Config{
				QuotaTenantConcurrency: c.configCap,
				QuotaUserConcurrency:   c.wantCap + 10, // not what this test is about
				ResolveEgressHosts:     blockingResolver(entered, release, stopErr),
			})

			tenant := uuid.New()
			holderErrs := make([]error, c.wantCap)
			var wg sync.WaitGroup
			for i := 0; i < c.wantCap; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_, err := eng.Launch(context.Background(), engineapi.Task{
						ID: uuid.New(), TenantID: tenant, UserID: uuid.New(), Pack: "coding-pack",
					})
					holderErrs[i] = err
				}(i)
			}

			// Deterministic barrier: proceed only once exactly wantCap launches
			// are confirmed holding a quota slot, so the excess attempt below
			// races against a saturated cap, never against scheduling luck.
			timeout := time.After(10 * time.Second)
			for i := 0; i < c.wantCap; i++ {
				select {
				case <-entered:
				case <-timeout:
					t.Fatalf("timed out waiting for %d concurrent launches to hold their quota slot (only %d entered)", c.wantCap, i)
				}
			}

			// The cap is now fully held. One more same-tenant attempt must be
			// rejected immediately with the specific quota error, not accepted
			// and not some other error.
			_, excessErr := eng.Launch(context.Background(), engineapi.Task{
				ID: uuid.New(), TenantID: tenant, UserID: uuid.New(), Pack: "coding-pack",
			})
			if !errors.Is(excessErr, quota.ErrTenantQuotaExceeded) {
				t.Fatalf("expected quota.ErrTenantQuotaExceeded for the (wantCap+1)th same-tenant launch, got: %v", excessErr)
			}

			// Release every held launch and confirm each one really did hold
			// the slot (its own error is the resolver's sentinel, not a quota
			// error) — proving the cap was enforced against real concurrent
			// holders, not against calls that had already failed for some
			// unrelated reason.
			close(release)
			wg.Wait()
			for i, err := range holderErrs {
				if !errors.Is(err, stopErr) {
					t.Fatalf("holder %d: expected the resolver's sentinel error, got: %v", i, err)
				}
			}

			// Quota slots are released on failure (Launch's own defer): if
			// they were not, this next same-tenant attempt would still see
			// the cap as saturated and get quota.ErrTenantQuotaExceeded
			// again instead of reaching the resolver. release is already
			// closed, so the shared resolver returns immediately.
			_, freshErr := eng.Launch(context.Background(), engineapi.Task{
				ID: uuid.New(), TenantID: tenant, UserID: uuid.New(), Pack: "coding-pack",
			})
			if !errors.Is(freshErr, stopErr) {
				t.Fatalf("expected quota slots to be released after failure, got: %v", freshErr)
			}
		})
	}
}

// TestStatusAndCancel_UnknownSessionReference would catch a regression where
// an unrecognized, malformed, or empty session reference produced a generic
// or nil-error "success" instead of the specific, documented
// engineapi.ErrUnknownSession — the error agenttask's poller and control-
// plane's Remote client (apps/control-plane/internal/agentengine/remote.go)
// both key off of to map onto a 404 / ErrEngineSessionGone.
func TestStatusAndCancel_UnknownSessionReference(t *testing.T) {
	cases := []struct {
		name       string
		sessionRef string
	}{
		{"well-formed but never launched", uuid.New().String()},
		{"not a UUID at all", "not-a-uuid"},
		{"empty string", ""},
	}

	eng := engineapi.New(engineapi.Config{})
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, _, _, err := eng.Status(context.Background(), c.sessionRef); !errors.Is(err, engineapi.ErrUnknownSession) {
				t.Fatalf("Status(%q): expected ErrUnknownSession, got: %v", c.sessionRef, err)
			}
			if err := eng.Cancel(context.Background(), c.sessionRef); !errors.Is(err, engineapi.ErrUnknownSession) {
				t.Fatalf("Cancel(%q): expected ErrUnknownSession, got: %v", c.sessionRef, err)
			}
		})
	}
}

// TestStatusConstants_MatchWireVocabulary locks the literal strings that
// actually cross the process boundary: cmd/agent-engine/serve.go's
// statusResponse.Status is string(status), and control-plane's
// apps/control-plane/internal/agentengine.Remote.Status decodes that same
// literal into its own agenttask.Status type. Nothing at compile time ties
// those two independently-declared string types together across the module
// boundary (engine.go's own package doc explains why), so only a value-level
// test like this one would catch a rename on one side silently breaking the
// other.
func TestStatusConstants_MatchWireVocabulary(t *testing.T) {
	want := map[engineapi.Status]string{
		engineapi.StatusQueued:    "queued",
		engineapi.StatusRunning:   "running",
		engineapi.StatusSucceeded: "succeeded",
		engineapi.StatusFailed:    "failed",
		engineapi.StatusCancelled: "cancelled",
	}
	for status, literal := range want {
		if string(status) != literal {
			t.Fatalf("expected %v to serialize as %q, got %q", status, literal, string(status))
		}
	}
}

// TestNew_NeverPanics documents the one invariant SandboxEngine.New relies on
// silently: a non-positive QuotaTenantConcurrency/QuotaUserConcurrency must be
// replaced by engine.Config.withDefaults before quota.New ever sees it, since
// quota.New itself rejects non-positive limits outright (see
// internal/quota/quota.go). New panics if that invariant is ever violated
// (engine.go: "Unreachable: withDefaults replaces every non-positive
// limit."); this test is the guard that keeps it unreachable across every
// input engineapi.Config actually allows a caller to construct.
func TestNew_NeverPanics(t *testing.T) {
	cases := []engineapi.Config{
		{},
		{QuotaTenantConcurrency: -1, QuotaUserConcurrency: -1},
		{QuotaTenantConcurrency: 0, QuotaUserConcurrency: 0},
		{QuotaTenantConcurrency: 1, QuotaUserConcurrency: 1},
	}
	for i, cfg := range cases {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("case %d: engineapi.New panicked: %v", i, r)
				}
			}()
			if eng := engineapi.New(cfg); eng == nil {
				t.Fatalf("case %d: expected a non-nil engine", i)
			}
		}()
	}
}
