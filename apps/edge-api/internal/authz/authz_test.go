package authz

import (
	"fmt"
	"testing"
	"time"
)

func TestCheckAccessActiveKey(t *testing.T) {
	s := AuthSnapshot{
		KeyID:     "key-1",
		AccountID: "acc-1",
		Status:    "active",
		AllowedAliases: []string{"hive-default", "hive-fast"},
		BudgetKind: "none",
	}

	r := CheckAccess(s, "hive-default", 0)
	if !r.Allowed {
		t.Fatalf("expected allowed, got denied: %s", r.DenyMsg)
	}
}

func TestCheckAccessRevokedKey(t *testing.T) {
	s := AuthSnapshot{
		KeyID:     "key-1",
		Status:    "revoked",
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
		KeyID:     "key-1",
		Status:    "active",
		ExpiresAt: &past,
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
		KeyID:                "key-1",
		Status:               "active",
		AllowAllModels:       true,
		BudgetKind:           "monthly",
		BudgetLimitCredits:   &limit,
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
		KeyID:                "key-1",
		Status:               "active",
		AllowAllModels:       true,
		BudgetKind:           "monthly",
		BudgetLimitCredits:   &limit,
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
		KeyID:  "key-1",
		Status: "disabled",
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
