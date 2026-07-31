package apikeys

import (
	"testing"

	"github.com/google/uuid"
)

// TestSoleCandidateFailsClosedOnAmbiguityOrAbsence is the regression guard
// for GetSoleNonCloudTenantID's Hive Enterprise resolution: zero candidates
// (no non-Cloud tenant yet) and more than one candidate (an ambiguous
// deployment, e.g. a mixed test fixture) must both collapse to uuid.Nil, so
// the caller fails closed instead of guessing which tenant an API key
// belongs to.
func TestSoleCandidateFailsClosedOnAmbiguityOrAbsence(t *testing.T) {
	single := uuid.New()
	multiA, multiB := uuid.New(), uuid.New()

	cases := []struct {
		name string
		in   []uuid.UUID
		want uuid.UUID
	}{
		{"absent", nil, uuid.Nil},
		{"exactly one", []uuid.UUID{single}, single},
		{"ambiguous", []uuid.UUID{multiA, multiB}, uuid.Nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := soleCandidate(tc.in); got != tc.want {
				t.Fatalf("soleCandidate(%v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}
