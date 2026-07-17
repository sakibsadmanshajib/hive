package embedmodel

import "testing"

func TestLookupByCanonicalID(t *testing.T) {
	m, ok := Lookup("bge-m3")
	if !ok || m.NativeDim != 1024 {
		t.Fatalf("Lookup(bge-m3) = %+v, %v; want NativeDim=1024, ok=true", m, ok)
	}
}

func TestLookupByLiteLLMRoute(t *testing.T) {
	m, ok := Lookup("route-openrouter-embedding-fallback")
	if !ok || m.NativeDim != 4096 {
		t.Fatalf("Lookup(route-openrouter-embedding-fallback) = %+v, %v; want NativeDim=4096, ok=true", m, ok)
	}
}

func TestLookupUnknownModel(t *testing.T) {
	if _, ok := Lookup("some-custom-model"); ok {
		t.Fatal("Lookup(some-custom-model) ok=true; want false for an unregistered model")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name       string
		model      string
		dim        int
		truncateTo int
		wantErr    bool
	}{
		{"native equals dim, no truncation: ok", "bge-m3", 1024, 0, false},
		{"native equals dim, truncation set: error", "bge-m3", 1024, 1024, true},
		{"native wider than dim, correct truncation: ok", "qwen3-embedding-8b", 1024, 1024, false},
		{"native wider than dim, truncation unset: error", "qwen3-embedding-8b", 1024, 0, true},
		{"native wider than dim, wrong truncation: error", "qwen3-embedding-8b", 1024, 512, true},
		{"native narrower than dim: always an error", "bge-m3", 2048, 0, true},
		{"native narrower than dim, truncation set: still an error", "bge-m3", 2048, 2048, true},
		{"unknown model: nothing to validate, ok", "custom-model-not-in-registry", 999, 0, false},
		{"lookup by LiteLLM route alias: ok", "route-openrouter-embedding-fallback", 1024, 1024, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.model, tc.dim, tc.truncateTo)
			if tc.wantErr && err == nil {
				t.Fatalf("Validate(%q, %d, %d) = nil, want an error", tc.model, tc.dim, tc.truncateTo)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Validate(%q, %d, %d) = %v, want nil", tc.model, tc.dim, tc.truncateTo, err)
			}
		})
	}
}
