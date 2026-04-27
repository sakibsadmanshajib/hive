package apikeys

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// These tests exercise the stubRepo's limits-shape semantics. They are not a
// substitute for the pgx integration test (handled by the migration smoke in
// 12-VERIFICATION.md), but they pin the contract that the production repo
// implementation must satisfy.

func TestStubRepoLimitsRoundTrip(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	keyID := uuid.New()

	repo.keys[keyID] = APIKey{ID: keyID, AccountID: accountID, Status: KeyStatusActive}

	// Default — no row written yet.
	limits, err := repo.GetLimits(context.Background(), accountID, keyID)
	if err != nil {
		t.Fatalf("GetLimits default: %v", err)
	}
	if limits.RPM != 60 || limits.TPM != 120000 {
		t.Fatalf("expected defaults rpm=60 tpm=120000, got rpm=%d tpm=%d", limits.RPM, limits.TPM)
	}

	// Update.
	in := KeyLimitsInput{
		RPM: 240,
		TPM: 80000,
		TierOverrides: map[string]TierLimit{
			"verified": {RPM: 200, TPM: 60000},
			"guest":    {RPM: 5, TPM: 1000},
		},
	}
	out, err := repo.UpdateLimits(context.Background(), accountID, keyID, in)
	if err != nil {
		t.Fatalf("UpdateLimits: %v", err)
	}
	if out.RPM != 240 || out.TPM != 80000 {
		t.Fatalf("UpdateLimits returned wrong values rpm=%d tpm=%d", out.RPM, out.TPM)
	}
	if got := out.TierOverrides["verified"].RPM; got != 200 {
		t.Fatalf("expected verified rpm=200, got %d", got)
	}

	// Re-read.
	limits, err = repo.GetLimits(context.Background(), accountID, keyID)
	if err != nil {
		t.Fatalf("GetLimits after update: %v", err)
	}
	if len(limits.TierOverrides) != 2 {
		t.Fatalf("expected 2 tier overrides, got %d: %#v", len(limits.TierOverrides), limits.TierOverrides)
	}
}

func TestStubRepoLimitsValidatesRanges(t *testing.T) {
	repo := newStubRepo()
	accountID := uuid.New()
	keyID := uuid.New()
	repo.keys[keyID] = APIKey{ID: keyID, AccountID: accountID, Status: KeyStatusActive}

	bad := []KeyLimitsInput{
		{RPM: -1, TPM: 1},
		{RPM: RateLimitRPMMax + 1, TPM: 1},
		{RPM: 1, TPM: -1},
		{RPM: 1, TPM: RateLimitTPMMax + 1},
		{RPM: 1, TPM: 1, TierOverrides: map[string]TierLimit{"platinum": {RPM: 1, TPM: 1}}},
		{RPM: 1, TPM: 1, TierOverrides: map[string]TierLimit{"verified": {RPM: -5, TPM: 1}}},
	}
	for i, in := range bad {
		_, err := repo.UpdateLimits(context.Background(), accountID, keyID, in)
		if !errors.Is(err, ErrLimitsOutOfRange) {
			t.Fatalf("case %d: expected ErrLimitsOutOfRange, got %v", i, err)
		}
	}
}

func TestStubRepoLimitsForeignAccountReturnsNotFound(t *testing.T) {
	repo := newStubRepo()
	accountA := uuid.New()
	accountB := uuid.New()
	keyID := uuid.New()
	repo.keys[keyID] = APIKey{ID: keyID, AccountID: accountA, Status: KeyStatusActive}

	_, err := repo.GetLimits(context.Background(), accountB, keyID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	_, err = repo.UpdateLimits(context.Background(), accountB, keyID, KeyLimitsInput{RPM: 60, TPM: 1000})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on update, got %v", err)
	}
}
