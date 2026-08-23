package agentsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	tenantA = uuid.New()
	userA   = uuid.New()
	idA     = uuid.New()
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// ptrTime is the test helper for *time.Time fields.
func ptrTime(t time.Time) *time.Time { return &t }

func TestService_Create_ValidatesAndComputesNextRun(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	svc := NewService(newFakeRepo(), fixedClock(now))
	ctx := context.Background()

	cases := []struct {
		in          CreateInput
		wantNextRun time.Duration
	}{
		{CreateInput{Name: "Morning digest", Instructions: "Summarize inbox", Schedule: "daily"}, 24 * time.Hour},
		{CreateInput{Name: "Weekly report", Instructions: "Draft report", Schedule: "weekly"}, 7 * 24 * time.Hour},
		{CreateInput{Name: "Watch", Instructions: "Check queue", Schedule: "interval:6"}, 6 * time.Hour},
	}
	for i, tc := range cases {
		got, err := svc.Create(ctx, tenantA, userA, tc.in)
		if err != nil {
			t.Fatalf("case %d Create: %v", i, err)
		}
		want := now.Add(tc.wantNextRun)
		if got.NextRunAt == nil || !got.NextRunAt.Equal(want) {
			t.Fatalf("case %d next run = %v, want %v", i, got.NextRunAt, want)
		}
		if !got.Enabled {
			t.Fatalf("case %d: new schedule must be enabled", i)
		}
	}
}

func TestService_Create_RejectsInvalidInput(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	svc := NewService(newFakeRepo(), fixedClock(now))
	ctx := context.Background()

	cases := []struct {
		in      CreateInput
		wantErr error
	}{
		{CreateInput{Name: "", Instructions: "x", Schedule: "daily"}, ErrInvalidName},
		{CreateInput{Name: strings.Repeat("a", 101), Instructions: "x", Schedule: "daily"}, ErrInvalidName},
		{CreateInput{Name: "n", Instructions: "", Schedule: "daily"}, ErrInvalidInstructions},
		{CreateInput{Name: "n", Instructions: strings.Repeat("a", 4001), Schedule: "daily"}, ErrInvalidInstructions},
		{CreateInput{Name: "n", Instructions: "x", Schedule: "* * * * *"}, ErrInvalidSchedule},
		{CreateInput{Name: "n", Instructions: "x", Schedule: "interval:0"}, ErrInvalidSchedule},
		{CreateInput{Name: "n", Instructions: "x", Schedule: "interval:99999"}, ErrInvalidSchedule},
		{CreateInput{Name: "n", Instructions: "x", Schedule: "interval:-3"}, ErrInvalidSchedule},
	}
	for i, tc := range cases {
		if _, err := svc.Create(ctx, tenantA, userA, tc.in); !errors.Is(err, tc.wantErr) {
			t.Errorf("case %d error = %v, want %v", i, err, tc.wantErr)
		}
	}
}

func TestService_SanitizeStripsControlCharsButKeepsNewlineAndTab(t *testing.T) {
	got := sanitizeInstructions("line1\r\nline2\tend\x00")
	want := "line1\nline2\tend"
	if got != want {
		t.Fatalf("sanitize = %q, want %q", got, want)
	}
}

func TestService_Update_RecomputesNextRunOnlyOnCadenceChange(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	repo := newFakeRepo(Schedule{
		ID: idA, TenantID: tenantA, UserID: userA,
		Name: "before", Instructions: "old", Schedule: "daily",
		Enabled:   true,
		NextRunAt: ptrTime(now.Add(24 * time.Hour)),
	})
	svc := NewService(repo, fixedClock(now))
	ctx := context.Background()

	out, err := svc.Update(ctx, tenantA, userA, idA, UpdateInput{
		Name: "after", Instructions: "new", Schedule: "daily", Enabled: true,
	})
	if err != nil {
		t.Fatalf("same-cadence update: %v", err)
	}
	if out.Name != "after" || out.Instructions != "new" {
		t.Fatal("expected fields replaced")
	}
	if out.NextRunAt == nil || !out.NextRunAt.Equal(now.Add(24*time.Hour)) {
		t.Fatal("same-cadence update must keep next_run_at")
	}

	out, err = svc.Update(ctx, tenantA, userA, idA, UpdateInput{
		Name: "after", Instructions: "new", Schedule: "weekly", Enabled: true,
	})
	if err != nil {
		t.Fatalf("cadence-change update: %v", err)
	}
	if out.NextRunAt == nil || !out.NextRunAt.Equal(now.Add(7*24*time.Hour)) {
		t.Fatal("cadence change must recompute next_run_at")
	}
}

func TestService_SetEnabled_ResetsNextRunWhenEnabling(t *testing.T) {
	now := time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC)
	stale := now.Add(-48 * time.Hour)
	repo := newFakeRepo(Schedule{
		ID: idA, TenantID: tenantA, UserID: userA,
		Name: "n", Instructions: "i", Schedule: "daily",
		Enabled:   false,
		NextRunAt: &stale,
	})
	svc := NewService(repo, fixedClock(now))
	ctx := context.Background()

	out, err := svc.SetEnabled(ctx, tenantA, userA, idA, true)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	if !out.Enabled || out.NextRunAt == nil || !out.NextRunAt.After(now) {
		t.Fatal("re-enable must push next_run_at into the future")
	}

	out, err = svc.SetEnabled(ctx, tenantA, userA, idA, false)
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	if out.Enabled {
		t.Fatal("expected disabled")
	}
}
