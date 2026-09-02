package ratewindows_test

import (
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/packages/ratewindows"
)

func TestBucketFloorsBeforeTheAnchor(t *testing.T) {
	anchor := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	week := ratewindows.WeeklyBucketSize

	cases := []struct {
		name string
		now  time.Time
		want int64
	}{
		{"at the anchor", anchor, 0},
		{"inside the first week", anchor.Add(3 * 24 * time.Hour), 0},
		{"one second before the second week", anchor.Add(week - time.Second), 0},
		{"the second week", anchor.Add(week), 1},
		// Truncating division would answer 0 here and put a pre-anchor
		// instant in the same bucket as the first real week, which would
		// silently merge two weeks of usage into one allowance.
		{"one second before the anchor", anchor.Add(-time.Second), -1},
	}
	for _, tc := range cases {
		if got := ratewindows.Bucket(tc.now, anchor, week); got != tc.want {
			t.Fatalf("%s: bucket %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestWeeklyResetLandsOnTheAnchor(t *testing.T) {
	anchor := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	shape := ratewindows.WeeklyShape()
	now := anchor.Add(50 * time.Hour)

	got := ratewindows.ResetAt(now, shape.EffectiveAnchor(anchor), shape.BucketSize, shape.Buckets)
	want := anchor.Add(ratewindows.WeeklyBucketSize)
	if !got.Equal(want) {
		t.Fatalf("weekly reset %s, want %s", got, want)
	}
}

func TestSessionWindowCoversFiveHours(t *testing.T) {
	shape := ratewindows.SessionShape()
	if span := time.Duration(shape.Buckets) * shape.BucketSize; span != 5*time.Hour {
		t.Fatalf("session window spans %s, want 5h", span)
	}
	now := time.Date(2026, time.September, 2, 12, 3, 0, 0, time.UTC)
	keys := shape.BucketKeys(ratewindows.AccountPrefix("acct-1"), now, time.Time{})
	if len(keys) != ratewindows.SessionBucketCount {
		t.Fatalf("got %d bucket keys, want %d", len(keys), ratewindows.SessionBucketCount)
	}
	if keys[0] == keys[1] {
		t.Fatalf("bucket keys are not distinct: %q", keys[0])
	}
}

// TestWeeklyAnchorRuleIsOneRule pins the shared rule both sides derive the
// weekly bucket key from. When these were two rules with two different fallback
// conditions, the limiter and the console read different Redis keys and the
// console reported zero percent used for a window the gateway was refusing on.
func TestWeeklyAnchorRuleIsOneRule(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	past := now.Add(-72 * time.Hour)

	if got, ok := ratewindows.ResolveWeeklyAnchor(past, now); !ok || !got.Equal(past) {
		t.Fatalf("a past anchor was rejected: %s %v", got, ok)
	}
	if _, ok := ratewindows.ResolveWeeklyAnchor(time.Time{}, now); ok {
		t.Fatal("the zero time was accepted as an anchor")
	}
	if _, ok := ratewindows.ResolveWeeklyAnchor(now.Add(time.Hour), now); ok {
		t.Fatal("a future anchor was accepted; it puts the account in a bucket before its own week zero")
	}

	text := past.Format(time.RFC3339)
	if got, ok := ratewindows.ParseWeeklyAnchor(&text, now); !ok || !got.Equal(past) {
		t.Fatalf("the wire form did not round trip: %s %v", got, ok)
	}
	if _, ok := ratewindows.ParseWeeklyAnchor(nil, now); ok {
		t.Fatal("an absent anchor was accepted; a stale auth snapshot carries none")
	}
	garbage := "not-a-time"
	if _, ok := ratewindows.ParseWeeklyAnchor(&garbage, now); ok {
		t.Fatal("an unparseable anchor was accepted")
	}
}
