package embedmodel

import "testing"

func TestLookupByCanonicalID(t *testing.T) {
	m, ok := Lookup("bge-m3")
	if !ok || m.NativeDim != 1024 || !m.MRL {
		t.Fatalf("Lookup(bge-m3) = %+v, %v; want NativeDim=1024, MRL=true, ok=true", m, ok)
	}
}

func TestLookupByLiteLLMRoute(t *testing.T) {
	m, ok := Lookup("route-openrouter-embedding-fallback")
	if !ok || m.NativeDim != 4096 || !m.MRL {
		t.Fatalf("Lookup(route-openrouter-embedding-fallback) = %+v, %v; want NativeDim=4096, MRL=true, ok=true", m, ok)
	}
}

func TestLookupNemotronNotMRL(t *testing.T) {
	m, ok := Lookup("nemotron-embed-vl-1b")
	if !ok || m.MRL {
		t.Fatalf("Lookup(nemotron-embed-vl-1b) = %+v, %v; want MRL=false (non-MRL, not selectable for indexed RAG)", m, ok)
	}
}

func TestLookupUnknownModel(t *testing.T) {
	if _, ok := Lookup("some-custom-model"); ok {
		t.Fatal("Lookup(some-custom-model) ok=true; want false for an unregistered model")
	}
}

// TestResolvePgvector locks the pgvector dimension-band mapping.
func TestResolvePgvector(t *testing.T) {
	cases := []struct {
		name           string
		dim            int
		allowUnindexed bool
		wantType       string
		wantOpclass    string
		wantIndexable  bool
		wantErr        bool
	}{
		{"1 -> vector indexable", 1, false, "vector", "vector_cosine_ops", true, false},
		{"1024 -> vector indexable", 1024, false, "vector", "vector_cosine_ops", true, false},
		{"2000 boundary -> vector indexable", 2000, false, "vector", "vector_cosine_ops", true, false},
		{"2001 -> halfvec indexable", 2001, false, "halfvec", "halfvec_cosine_ops", true, false},
		{"3000 -> halfvec indexable", 3000, false, "halfvec", "halfvec_cosine_ops", true, false},
		{"4000 boundary -> halfvec indexable", 4000, false, "halfvec", "halfvec_cosine_ops", true, false},
		{"4096 without opt-in -> reject", 4096, false, "", "", false, true},
		{"4096 with opt-in -> halfvec unindexed", 4096, true, "halfvec", "", false, false},
		{"16000 with opt-in -> halfvec unindexed", 16000, true, "halfvec", "", false, false},
		{"16001 -> reject even with opt-in", 16001, true, "", "", false, true},
		{"zero -> reject", 0, false, "", "", false, true},
		{"negative -> reject", -5, false, "", "", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pgType, opclass, indexable, err := ResolvePgvector(tc.dim, tc.allowUnindexed)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolvePgvector(%d,%v) = nil error, want error", tc.dim, tc.allowUnindexed)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePgvector(%d,%v) unexpected error: %v", tc.dim, tc.allowUnindexed, err)
			}
			if pgType != tc.wantType || opclass != tc.wantOpclass || indexable != tc.wantIndexable {
				t.Fatalf("ResolvePgvector(%d,%v) = (%q,%q,%v), want (%q,%q,%v)",
					tc.dim, tc.allowUnindexed, pgType, opclass, indexable,
					tc.wantType, tc.wantOpclass, tc.wantIndexable)
			}
		})
	}
}

// TestResolvePolicy is the selectable model/dimension policy table from the
// plan: which (model, dim) combinations the system accepts, and what each
// accepted one resolves to.
func TestResolvePolicy(t *testing.T) {
	cases := []struct {
		name           string
		model          string
		dim            int
		allowUnindexed bool
		wantErr        bool
		wantType       string
		wantOpclass    string
		wantReduceTo   int
		wantIndexable  bool
	}{
		{
			name: "bge-m3 native 1024 -> vector HNSW, no reduction",
			model: "bge-m3", dim: 1024,
			wantType: "vector", wantOpclass: "vector_cosine_ops", wantReduceTo: 0, wantIndexable: true,
		},
		{
			name: "qwen3 @1024 (MRL reduce) -> vector HNSW, reduce to 1024 (clean demo target)",
			model: "qwen3-embedding-8b", dim: 1024,
			wantType: "vector", wantOpclass: "vector_cosine_ops", wantReduceTo: 1024, wantIndexable: true,
		},
		{
			name: "qwen3 @3000 (MRL reduce) -> halfvec HNSW",
			model: "qwen3-embedding-8b", dim: 3000,
			wantType: "halfvec", wantOpclass: "halfvec_cosine_ops", wantReduceTo: 3000, wantIndexable: true,
		},
		{
			name: "qwen3 @4096 native without opt-in -> reject (no index possible)",
			model: "qwen3-embedding-8b", dim: 4096, wantErr: true,
		},
		{
			name: "qwen3 @4096 native with opt-in -> halfvec brute-force",
			model: "qwen3-embedding-8b", dim: 4096, allowUnindexed: true,
			wantType: "halfvec", wantOpclass: "", wantReduceTo: 0, wantIndexable: false,
		},
		{
			name: "nemotron @1024 (non-MRL reduce) -> REJECT (owner mandate: no naive truncation)",
			model: "nemotron-embed-vl-1b", dim: 1024, wantErr: true,
		},
		{
			name: "nemotron @4096 native without opt-in -> reject (non-MRL, no index)",
			model: "nemotron-embed-vl-1b", dim: 4096, wantErr: true,
		},
		{
			name: "qwen3 @8192 wider than native -> reject (cannot invent dimensions)",
			model: "qwen3-embedding-8b", dim: 8192, allowUnindexed: true, wantErr: true,
		},
		{
			name: "bge-m3 @2048 wider than native -> reject",
			model: "bge-m3", dim: 2048, wantErr: true,
		},
		{
			name: "unknown model @1024 -> accepted, resolves pgvector by dim, no reduction",
			model: "custom-model-not-in-registry", dim: 1024,
			wantType: "vector", wantOpclass: "vector_cosine_ops", wantReduceTo: 0, wantIndexable: true,
		},
		{
			name: "dim zero -> reject even for unknown model",
			model: "custom-model-not-in-registry", dim: 0, wantErr: true,
		},
		{
			name: "route alias resolves to qwen3 facts (MRL reduce ok)",
			model: "route-openrouter-embedding-fallback", dim: 1024,
			wantType: "vector", wantOpclass: "vector_cosine_ops", wantReduceTo: 1024, wantIndexable: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Resolve(tc.model, tc.dim, tc.allowUnindexed)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("Resolve(%q,%d,%v) = %+v, nil error; want an error", tc.model, tc.dim, tc.allowUnindexed, p)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve(%q,%d,%v) unexpected error: %v", tc.model, tc.dim, tc.allowUnindexed, err)
			}
			if p.PgType != tc.wantType || p.Opclass != tc.wantOpclass || p.ReduceTo != tc.wantReduceTo || p.Indexable != tc.wantIndexable {
				t.Fatalf("Resolve(%q,%d,%v) = {PgType:%q Opclass:%q ReduceTo:%d Indexable:%v}, want {PgType:%q Opclass:%q ReduceTo:%d Indexable:%v}",
					tc.model, tc.dim, tc.allowUnindexed,
					p.PgType, p.Opclass, p.ReduceTo, p.Indexable,
					tc.wantType, tc.wantOpclass, tc.wantReduceTo, tc.wantIndexable)
			}
		})
	}
}

func TestCanonical(t *testing.T) {
	cases := map[string]string{
		"route-openrouter-embedding-fallback": "qwen3-embedding-8b",
		"qwen3-embedding-8b":                  "qwen3-embedding-8b",
		"route-openrouter-embedding":          "nemotron-embed-vl-1b",
		"bge-m3":                              "bge-m3",
		"some-unknown-model":                  "some-unknown-model",
	}
	for in, want := range cases {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlanColumnType(t *testing.T) {
	p, err := Resolve("qwen3-embedding-8b", 3000, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := p.ColumnType(); got != "halfvec(3000)" {
		t.Fatalf("ColumnType() = %q, want halfvec(3000)", got)
	}
}

// TestValidateWrapsResolve confirms Validate agrees with Resolve on accept/
// reject so the UI (which will call Validate) and the provisioning path (which
// calls Resolve) can never disagree.
// TestSameConfig is the #368 cross-service reconcile: both services run this
// same comparison, so a matched pair keeps RAG enabled and a mismatched pair
// (different model or dim) disables it. Route alias and canonical id match.
func TestSameConfig(t *testing.T) {
	cases := []struct {
		name             string
		aModel           string
		aDim             int
		bModel           string
		bDim             int
		want             bool
	}{
		{"identical", "bge-m3", 1024, "bge-m3", 1024, true},
		{"route alias vs canonical, same model", "route-openrouter-embedding-fallback", 1024, "qwen3-embedding-8b", 1024, true},
		{"different model, same dim -> mismatch", "bge-m3", 1024, "qwen3-embedding-8b", 1024, false},
		{"same model, different dim -> mismatch", "qwen3-embedding-8b", 1024, "qwen3-embedding-8b", 1536, false},
		{"two unknown equal customs", "custom-x", 768, "custom-x", 768, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameConfig(tc.aModel, tc.aDim, tc.bModel, tc.bDim); got != tc.want {
				t.Fatalf("SameConfig(%q,%d,%q,%d) = %v, want %v", tc.aModel, tc.aDim, tc.bModel, tc.bDim, got, tc.want)
			}
		})
	}
}

func TestValidateWrapsResolve(t *testing.T) {
	if err := Validate("nemotron-embed-vl-1b", 1024, false); err == nil {
		t.Fatal("Validate(nemotron,1024) = nil; want reject (non-MRL reduction)")
	}
	if err := Validate("qwen3-embedding-8b", 1024, false); err != nil {
		t.Fatalf("Validate(qwen3,1024) = %v; want nil (MRL reduction ok)", err)
	}
}
