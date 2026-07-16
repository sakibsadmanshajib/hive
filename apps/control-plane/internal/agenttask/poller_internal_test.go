package agenttask

import (
	"testing"
	"time"
)

func TestPollerBackoffDelay(t *testing.T) {
	base := 15 * time.Second
	cases := []struct {
		consecutiveFailures int
		want                time.Duration
	}{
		{0, 15 * time.Second},
		{1, 30 * time.Second},
		{2, 1 * time.Minute},
		{3, 2 * time.Minute},
		{4, 4 * time.Minute},
		{5, maxPollerBackoff}, // 8m would exceed the 5m cap
		{20, maxPollerBackoff},
	}
	for _, tc := range cases {
		got := pollerBackoffDelay(base, tc.consecutiveFailures)
		if got != tc.want {
			t.Errorf("pollerBackoffDelay(%s, %d) = %s, want %s", base, tc.consecutiveFailures, got, tc.want)
		}
	}
}
