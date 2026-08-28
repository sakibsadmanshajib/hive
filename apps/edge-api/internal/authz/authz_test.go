package authz

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCheckAccessActiveKey(t *testing.T) {
	s := AuthSnapshot{
		KeyID:          "key-1",
		AccountID:      "acc-1",
		Status:         "active",
		AllowedAliases: []string{"hive-default", "hive-fast"},
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed, got denied: %s", r.DenyMsg)
	}
}

func TestCheckAccessRevokedKey(t *testing.T) {
	s := AuthSnapshot{
		KeyID:      "key-1",
		Status:     "revoked",
		BudgetKind: "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if r.Allowed {
		t.Fatal("expected denied for revoked key")
	}
	if r.DenyCode != "invalid_api_key" {
		t.Fatalf("expected invalid_api_key code, got %s", r.DenyCode)
	}
}

func TestCheckAccessExpiredKey(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).Format(time.RFC3339)
	s := AuthSnapshot{
		KeyID:      "key-1",
		Status:     "active",
		ExpiresAt:  &past,
		BudgetKind: "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if r.Allowed {
		t.Fatal("expected denied for expired key")
	}
	if r.DenyCode != "invalid_api_key" {
		t.Fatalf("expected invalid_api_key code, got %s", r.DenyCode)
	}
}

// TestCheckAccessDeniesUnparseableExpiresAt pins the hardening fix at
// authz.go's expiry check: a present-but-unparseable expires_at must deny
// the request, not silently skip the expiry check (fail-open on a parse
// error). See authz.go step 2 for the reasoning on why this is defence in
// depth rather than a fix for a reachable gap (issue #915 / PR #919
// established expiry enforcement already works via two independent layers
// on every real value control-plane emits).
func TestCheckAccessDeniesUnparseableExpiresAt(t *testing.T) {
	bad := "not-a-timestamp"
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		ExpiresAt:      &bad,
		AllowAllModels: true,
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if r.Allowed {
		t.Fatal("expected denied for unparseable expires_at, got allowed (fail-open on parse error)")
	}
	if r.DenyCode != "invalid_api_key" {
		t.Fatalf("expected invalid_api_key code, got %s", r.DenyCode)
	}
	// Both this arm and the genuinely-expired arm return the same DenyCode,
	// so DenyMsg is the only field an operator reading logs can use to tell
	// a malformed cache entry apart from routine expiry. Pin it, or the
	// message could silently drift to "API key has expired" and this test
	// (and every other test in the package) would still pass.
	if r.DenyMsg != "API key has an invalid expiry" {
		t.Fatalf("expected the malformed-record message, got %q", r.DenyMsg)
	}
}

// TestCheckAccessEmptyExpiresAtStillAllowed pins the "no live outage" side
// of the same change: an empty expires_at string is treated the same as an
// absent one (no expiry set), and must keep authorizing exactly as it does
// today. Only a NON-empty unparseable value is denied.
func TestCheckAccessEmptyExpiresAtStillAllowed(t *testing.T) {
	empty := ""
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		ExpiresAt:      &empty,
		AllowAllModels: true,
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed for empty expires_at (treated as absent), got denied: %s", r.DenyMsg)
	}
}

// TestCheckAccessWhitespaceOnlyExpiresAtStillAllowed covers the distinct
// branch TrimSpace introduces: a whitespace-only expires_at is trimmed down
// to empty and takes the same no-expiry path as an empty string, rather than
// being handed to time.Parse as a non-empty value that would fail.
func TestCheckAccessWhitespaceOnlyExpiresAtStillAllowed(t *testing.T) {
	whitespace := " \t\n"
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		ExpiresAt:      &whitespace,
		AllowAllModels: true,
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed for whitespace-only expires_at, got denied: %s", r.DenyMsg)
	}
}

// TestCheckAccessNilExpiresAtStillAllowed pins the other "no live outage"
// side: a key with no expires_at set at all (nil pointer, the common case
// for a non-expiring key) must keep authorizing exactly as it does today.
func TestCheckAccessNilExpiresAtStillAllowed(t *testing.T) {
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		ExpiresAt:      nil,
		AllowAllModels: true,
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed for nil expires_at (no expiry set), got denied: %s", r.DenyMsg)
	}
}

func TestCheckAccessRejectsDisallowedAliasWithoutRemap(t *testing.T) {
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		AllowedAliases: []string{"hive-default"},
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "hive-auto", 0)
	if r.Allowed {
		t.Fatal("expected denied for disallowed model")
	}
	if r.DenyCode != "model_not_allowed" {
		t.Fatalf("expected model_not_allowed code, got %s", r.DenyCode)
	}
}

func TestCheckAccessAllModelsWildcard(t *testing.T) {
	s := AuthSnapshot{
		KeyID:          "key-1",
		Status:         "active",
		AllowAllModels: true,
		BudgetKind:     "none",
	}

	r := CheckAccess(s, "any-model", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed with all-models, got denied: %s", r.DenyMsg)
	}
}

func TestCheckAccessRejectsProjectedBudgetOverrun(t *testing.T) {
	limit := int64(1000)
	s := AuthSnapshot{
		KeyID:                 "key-1",
		Status:                "active",
		AllowAllModels:        true,
		BudgetKind:            "monthly",
		BudgetLimitCredits:    &limit,
		BudgetConsumedCredits: 850,
		BudgetReservedCredits: 100,
	}

	r := CheckAccess(s, "hive-default", 100)
	if r.Allowed {
		t.Fatal("expected denied for projected budget overrun")
	}
	if r.DenyCode != "budget_exceeded" {
		t.Fatalf("expected budget_exceeded code, got %s", r.DenyCode)
	}
}

func TestCheckAccessBudgetWithinLimit(t *testing.T) {
	limit := int64(1000)
	s := AuthSnapshot{
		KeyID:                 "key-1",
		Status:                "active",
		AllowAllModels:        true,
		BudgetKind:            "monthly",
		BudgetLimitCredits:    &limit,
		BudgetConsumedCredits: 400,
		BudgetReservedCredits: 100,
	}

	r := CheckAccess(s, "hive-default", 200)
	if !r.Allowed {
		t.Fatalf("expected allowed within budget, got denied: %s", r.DenyMsg)
	}
}

func TestCheckAccessDenyReasonFormat(t *testing.T) {
	s := AuthSnapshot{
		KeyID:      "key-1",
		Status:     "disabled",
		BudgetKind: "none",
	}

	r := CheckAccess(s, "", 0)
	if r.Allowed {
		t.Fatal("expected denied for disabled key")
	}
	expected := fmt.Sprintf("API key is %s", "disabled")
	if r.DenyMsg != expected {
		t.Fatalf("expected message %q, got %q", expected, r.DenyMsg)
	}
}

// TestTenantUUIDLogsOnAccountNotProvisioned is the regression guard for the
// live incident on 2026-08-28: a security reviewer's freshly minted API key
// answered 403 account_not_provisioned with nothing anywhere -- not a log
// line, not a metric -- to tell an operator it happened. TenantUUID is the
// single choke point every fail-closed caller (handleModels,
// inference.Orchestrator.selectRoute, checkOWUIShimKey) routes through, so
// the log line belongs here once, not duplicated at each call site.
func TestTenantUUIDLogsOnAccountNotProvisioned(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	s := AuthSnapshot{KeyID: "key-repro", AccountID: "acct-repro", TenantID: ""}
	if _, err := s.TenantUUID(); !errors.Is(err, ErrAccountNotProvisioned) {
		t.Fatalf("expected ErrAccountNotProvisioned, got %v", err)
	}

	got := buf.String()
	for _, want := range []string{"account_not_provisioned", "acct-repro", "key-repro"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected log output to contain %q, got %q", want, got)
		}
	}
}

// TestTenantUUIDSilentOnSuccess guards the inverse: a resolved tenant must
// not spam the log on every ordinary request.
func TestTenantUUIDSilentOnSuccess(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	s := AuthSnapshot{KeyID: "key-ok", AccountID: "acct-ok", TenantID: uuid.New().String()}
	if _, err := s.TenantUUID(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no log output on success, got %q", buf.String())
	}
}
