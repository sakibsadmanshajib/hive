package rag

import (
	"strings"
	"testing"
)

// TestSearchChunksQueryCast is the regression guard for the halfvec query path:
// a halfvec-provisioned column must be queried with ::halfvec, a vector column
// with ::vector. pgvector's vector<->halfvec cast is assignment-only, so the
// wrong cast has no <=> operator and errors at query time. No live DB needed:
// searchChunksQuery is a pure builder over embedmodel.Cast.
func TestSearchChunksQueryCast(t *testing.T) {
	cases := []struct {
		name    string
		pgType  string
		want    string
		notWant string
	}{
		{"halfvec column", "halfvec", "$1::halfvec", "$1::vector"},
		{"vector column", "vector", "$1::vector", "$1::halfvec"},
		{"empty defaults to vector", "", "$1::vector", "$1::halfvec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := searchChunksQuery(tc.pgType)
			if !strings.Contains(q, tc.want) {
				t.Fatalf("searchChunksQuery(%q) missing %q:\n%s", tc.pgType, tc.want, q)
			}
			if strings.Contains(q, tc.notWant) {
				t.Fatalf("searchChunksQuery(%q) unexpectedly contains %q:\n%s", tc.pgType, tc.notWant, q)
			}
			// Both distance references (SELECT score and ORDER BY) must carry
			// the cast, else one side falls back to the column default type.
			if got := strings.Count(q, tc.want); got != 2 {
				t.Fatalf("searchChunksQuery(%q): want cast %q twice, got %d:\n%s", tc.pgType, tc.want, got, q)
			}
		})
	}
}
