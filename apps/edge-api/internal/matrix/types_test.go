package matrix

import "testing"

// TestHasCoverage exercises the two mismatches between HasCoverage and
// Lookup a reviewer found on the boot-time drift guard's PR: an exact
// (non-subtree) mux pattern must not be satisfied by a descendant matrix
// entry it cannot actually dispatch to, and a subtree mux pattern must not
// be satisfied by an entry sitting exactly at the trimmed prefix instead of
// under it. Both cases pass silently under the old, buggier HasCoverage:
// that is exactly why the guard could pass boot while
// UnsupportedEndpointMiddleware still 404s the same registered route.
func TestHasCoverage(t *testing.T) {
	m, err := LoadMatrixFromBytes([]byte(testMatrixJSON))
	if err != nil {
		t.Fatalf("LoadMatrixFromBytes failed: %v", err)
	}

	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{
			name:    "exact pattern is not covered by a descendant templated entry",
			pattern: "/v1/files",
			want:    false,
		},
		{
			name:    "subtree pattern is not covered by an entry sitting exactly at the trimmed prefix",
			pattern: "/v1/models/",
			want:    false,
		},
		{
			name:    "exact pattern matches an exact entry",
			pattern: "/v1/models",
			want:    true,
		},
		{
			name:    "subtree pattern matches a genuine descendant entry",
			pattern: "/v1/files/",
			want:    true,
		},
		{
			name:    "exact pattern with zero matrix awareness at all is not covered",
			pattern: "/v1/unknown",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.HasCoverage(tt.pattern)
			if got != tt.want {
				t.Errorf("HasCoverage(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

// TestHasCoverageAgreesWithLookupForExactPatterns asserts, for a table of
// non-subtree mux patterns, that HasCoverage's yes/no answer matches whether
// Lookup finds the pattern known to the matrix at all (any status, since
// HasCoverage disregards status by design). This is the structural version
// of the two cases above: it catches future drift between the two matchers
// without anyone having to remember to hand-add a new mismatch case.
func TestHasCoverageAgreesWithLookupForExactPatterns(t *testing.T) {
	m, err := LoadMatrixFromBytes([]byte(testMatrixJSON))
	if err != nil {
		t.Fatalf("LoadMatrixFromBytes failed: %v", err)
	}

	patterns := []struct {
		pattern string
		method  string
	}{
		{"/v1/models", "GET"},
		{"/v1/models", "POST"},
		{"/v1/chat/completions", "POST"},
		{"/v1/assistants", "GET"},
		{"/v1/organization/users", "GET"},
		{"/v1/files", "GET"},
		{"/v1/batches", "GET"},
		{"/v1/unknown", "GET"},
	}

	for _, p := range patterns {
		t.Run(p.method+" "+p.pattern, func(t *testing.T) {
			covered := m.HasCoverage(p.pattern)
			known := m.Lookup(p.method, p.pattern) != StatusUnknown
			if covered != known {
				t.Errorf("HasCoverage(%q) = %v but Lookup(%q, %q) known = %v (status %q); the two must agree on whether the matrix knows this exact path",
					p.pattern, covered, p.method, p.pattern, known, m.Lookup(p.method, p.pattern))
			}
		})
	}
}
