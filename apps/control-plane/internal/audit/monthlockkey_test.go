package audit_test

import (
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/audit"
)

// TestMonthLockKey_PinnedValues pins audit.MonthLockKey against known
// timestamps, including a UTC month boundary and two non-UTC-zoned
// timestamps that land on the opposite side of a month boundary from
// their own local calendar date. This is what stops a future change
// from reintroducing a dependency on the caller's (or Postgres's
// session) timezone: MonthLockKey must bucket purely on the instant's
// UTC month, not on whatever Location happens to be attached to the
// time.Time it's given (#1188 review thread).
func TestMonthLockKey_PinnedValues(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}

	tests := []struct {
		name string
		ts   time.Time
		want time.Time // UTC month-start the key must correspond to
	}{
		{
			name: "UTC mid-month",
			ts:   time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			want: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			name: "UTC exact month boundary",
			ts:   time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			want: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// 2026-01-31 23:30 America/New_York (EST, UTC-5) is
			// 2026-02-01 04:30 UTC: the local calendar date is still
			// January, but the UTC instant is already February.
			name: "non-UTC zone: local date lags the UTC month",
			ts:   time.Date(2026, 1, 31, 23, 30, 0, 0, newYork),
			want: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// 2026-06-01 03:00 Asia/Tokyo (JST, UTC+9) is
			// 2026-05-31 18:00 UTC: the local calendar date is
			// already June, but the UTC instant is still May.
			name: "non-UTC zone: local date leads the UTC month",
			ts:   time.Date(2026, 6, 1, 3, 0, 0, 0, tokyo),
			want: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := audit.MonthLockKey(tc.ts)
			want := tc.want.Unix()
			if got != want {
				t.Errorf("MonthLockKey(%v) = %d, want %d (%v)", tc.ts, got, want, tc.want)
			}
		})
	}
}
