package main

import (
	"math"
	"strings"
	"testing"
)

func TestMemoryLimitWarning(t *testing.T) {
	const gib = int64(1) << 30

	tests := []struct {
		name      string
		soft      int64
		hard      int64
		wantMatch string
	}{
		{
			// What deploy/docker/docker-compose.yml ships: 1280 MiB under 1536 MiB.
			name: "shipped pair is silent",
			soft: 1280 << 20,
			hard: 1536 << 20,
		},
		{
			name:      "soft at the hard limit cannot help",
			soft:      gib,
			hard:      gib,
			wantMatch: "at or above",
		},
		{
			name:      "soft above the hard limit cannot help",
			soft:      2 * gib,
			hard:      gib,
			wantMatch: "at or above",
		},
		{
			name:      "too little headroom for what GOMEMLIMIT does not count",
			soft:      gib - (10 << 20),
			hard:      gib,
			wantMatch: "leaves only",
		},
		{
			name: "no cgroup limit readable",
			soft: gib,
			hard: 0,
		},
		{
			name: "GOMEMLIMIT unset",
			soft: math.MaxInt64,
			hard: gib,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryLimitWarning(tc.soft, tc.hard)
			if tc.wantMatch == "" {
				if got != "" {
					t.Fatalf("want no warning, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.wantMatch) {
				t.Fatalf("warning %q does not mention %q", got, tc.wantMatch)
			}
		})
	}
}

func TestParseCgroupMemoryMax(t *testing.T) {
	if got := parseCgroupMemoryMax("1610612736\n"); got != 1610612736 {
		t.Fatalf("parse = %d, want 1610612736", got)
	}
	// "max" means unlimited, and must read as "nothing to compare against"
	// rather than as a limit of zero that warns on every boot.
	if got := parseCgroupMemoryMax("max\n"); got != 0 {
		t.Fatalf(`parse("max") = %d, want 0`, got)
	}
	if got := parseCgroupMemoryMax(""); got != 0 {
		t.Fatalf(`parse("") = %d, want 0`, got)
	}
}
