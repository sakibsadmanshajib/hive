package catalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func boolPtr(v bool) *bool { return &v }

// TestAliasVisibleToTenantRules pins the entitlement predicate. This is the one
// function both the catalog listing and the inference-time check resolve every
// verdict through, so these cases are the whole contract.
func TestAliasVisibleToTenantRules(t *testing.T) {
	tests := []struct {
		name       string
		visibility string
		override   *bool
		want       bool
	}{
		{name: "public with no row is entitled", visibility: "public", override: nil, want: true},
		{name: "preview with no row is entitled", visibility: "preview", override: nil, want: true},
		{name: "public blocked by explicit false row", visibility: "public", override: boolPtr(false), want: false},
		{name: "preview blocked by explicit false row", visibility: "preview", override: boolPtr(false), want: false},
		{name: "public with explicit true row stays entitled", visibility: "public", override: boolPtr(true), want: true},
		{name: "restricted with no row is refused", visibility: "restricted", override: nil, want: false},
		{name: "restricted with explicit true row is entitled", visibility: "restricted", override: boolPtr(true), want: true},
		{name: "restricted with explicit false row is refused", visibility: "restricted", override: boolPtr(false), want: false},
		{name: "internal is never entitled", visibility: "internal", override: nil, want: false},
		{name: "internal is not entitled even with a true row", visibility: "internal", override: boolPtr(true), want: false},
		{name: "unknown visibility class is refused", visibility: "experimental", override: nil, want: false},
		{name: "visibility class is compared case and space insensitively", visibility: "  Public  ", override: nil, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := AliasVisibleToTenant(tt.visibility, tt.override); got != tt.want {
				t.Fatalf("AliasVisibleToTenant(%q, %v) = %v, want %v", tt.visibility, tt.override, got, tt.want)
			}
		})
	}
}

// TestZeroVisibilityRowsKeepsPublicAndPreviewAliases is the production-safety
// regression: tenant_model_visibility ships empty, so a tenant with no rows at
// all must keep every public and preview alias. A deny-by-default predicate
// would revoke chat for every existing tenant on every deployment.
func TestZeroVisibilityRowsKeepsPublicAndPreviewAliases(t *testing.T) {
	rows := []aliasWithOverride{
		{alias: ModelAlias{AliasID: "hive-default", Visibility: "public"}},
		{alias: ModelAlias{AliasID: "hive-preview", Visibility: "preview"}},
		{alias: ModelAlias{AliasID: "hive-locked", Visibility: "restricted"}},
		{alias: ModelAlias{AliasID: "hive-internal", Visibility: "internal"}},
	}

	visible := filterVisibleForTenant(rows)

	got := make([]string, 0, len(visible))
	for _, alias := range visible {
		got = append(got, alias.AliasID)
	}
	want := []string{"hive-default", "hive-preview"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("zero visibility rows must keep public+preview only, got %v want %v", got, want)
	}
}

// TestCatalogListAndInferenceVerdictAgree pins the listing verdict and the
// single-alias entitlement verdict to the same answer across a table of cases.
// Both production paths call filterVisibleForTenant; the only difference is
// whether the SQL narrowed the row set to one alias first, which is exactly
// what this test varies.
func TestCatalogListAndInferenceVerdictAgree(t *testing.T) {
	rows := []aliasWithOverride{
		{alias: ModelAlias{AliasID: "public-no-row", Visibility: "public"}},
		{alias: ModelAlias{AliasID: "public-blocked", Visibility: "public"}, override: boolPtr(false)},
		{alias: ModelAlias{AliasID: "preview-no-row", Visibility: "preview"}},
		{alias: ModelAlias{AliasID: "preview-blocked", Visibility: "preview"}, override: boolPtr(false)},
		{alias: ModelAlias{AliasID: "restricted-no-row", Visibility: "restricted"}},
		{alias: ModelAlias{AliasID: "restricted-granted", Visibility: "restricted"}, override: boolPtr(true)},
		{alias: ModelAlias{AliasID: "restricted-blocked", Visibility: "restricted"}, override: boolPtr(false)},
		{alias: ModelAlias{AliasID: "internal-alias", Visibility: "internal"}},
	}

	listed := make(map[string]bool, len(rows))
	for _, alias := range filterVisibleForTenant(rows) {
		listed[alias.AliasID] = true
	}

	for _, row := range rows {
		t.Run(row.alias.AliasID, func(t *testing.T) {
			// The inference path queries the same view narrowed to one alias.
			single := filterVisibleForTenant([]aliasWithOverride{row})
			invokable := len(single) == 1

			if invokable != listed[row.alias.AliasID] {
				t.Fatalf("alias %q: listed=%v but invokable=%v; the catalog list and the inference verdict must agree",
					row.alias.AliasID, listed[row.alias.AliasID], invokable)
			}
		})
	}
}

// TestIsAliasVisibleToTenantRefusesEmptyAliasID pins the fail-closed guard. An
// empty alias id is the "every alias" filter value for the shared query, so
// without the guard the membership check would pass for any tenant entitled to
// at least one model. The nil pool asserts the guard returns before any query.
func TestIsAliasVisibleToTenantRefusesEmptyAliasID(t *testing.T) {
	repo := &pgxRepository{}

	for _, aliasID := range []string{"", "   "} {
		visible, err := repo.IsAliasVisibleToTenant(context.Background(), uuid.New(), aliasID)
		if err != nil {
			t.Fatalf("IsAliasVisibleToTenant(%q) returned error: %v", aliasID, err)
		}
		if visible {
			t.Fatalf("IsAliasVisibleToTenant(%q) = true, want false (must fail closed)", aliasID)
		}
	}
}

// TestTenantVisibilityQueryHasSingleSource guards the mechanism that keeps the
// two verdicts identical: exactly one query reads tenant_model_visibility for
// entitlement, and both entry points route through it.
func TestTenantVisibilityQueryHasSingleSource(t *testing.T) {
	source := readCatalogRepositorySource(t)

	// The verdict has exactly one caller: filterVisibleForTenant. A second call
	// site would be a second copy of the entitlement decision.
	// Subtract the IsAliasVisibleToTenant declarations, which merely contain the
	// predicate name as a substring.
	got := strings.Count(source, "AliasVisibleToTenant(") - strings.Count(source, "IsAliasVisibleToTenant(")
	if got != 1 {
		t.Fatalf("expected exactly one AliasVisibleToTenant call in repository.go, got %d", got)
	}
	if !strings.Contains(methodBody(t, source, "func filterVisibleForTenant"), "AliasVisibleToTenant(") {
		t.Fatal("filterVisibleForTenant must be the one place the entitlement predicate is applied")
	}
	for _, method := range []string{"func (r *pgxRepository) ListAliasesForTenant", "func (r *pgxRepository) IsAliasVisibleToTenant"} {
		body := methodBody(t, source, method)
		if !strings.Contains(body, "queryTenantVisibility") {
			t.Fatalf("%s must resolve entitlement through queryTenantVisibility, not its own predicate", method)
		}
		if strings.Contains(body, "SELECT") {
			t.Fatalf("%s must not carry its own SQL; the entitlement query has one source", method)
		}
	}
}

func readCatalogRepositorySource(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wd, "repository.go"))
	if err != nil {
		t.Fatalf("read repository.go: %v", err)
	}
	return string(data)
}

func methodBody(t *testing.T, source, signature string) string {
	t.Helper()
	start := strings.Index(source, signature)
	if start == -1 {
		t.Fatalf("method %q not found in repository.go", signature)
	}
	rest := source[start+len(signature):]
	end := strings.Index(rest, "\nfunc ")
	if end == -1 {
		return rest
	}
	return rest[:end]
}
