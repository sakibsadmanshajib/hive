// Package embedmodel is the single source of truth for which embedding
// models Hive knows about, how wide a vector each one natively produces,
// whether it is Matryoshka-trained (so a shorter dimension is legitimate),
// and which LiteLLM route (if any) serves it. Both apps/edge-api and
// apps/control-plane import this instead of keeping their own copy, so the
// bge-m3/nemotron/qwen3 dimension facts cannot drift between the query path
// and the ingest path (#368).
//
// This package answers three questions for the admin-selectable embedding
// redesign:
//
//   - Is a chosen (model, dim) serviceable at all? (Resolve/Validate)
//   - Which pgvector column type and HNSW opclass does that dimension map to?
//     (ResolvePgvector)
//   - Does the chosen dimension require a client-side MRL reduction, and to
//     what width? (Plan.ReduceTo)
//
// The owner mandate is explicit: no naive truncation band-aid. A non-MRL
// model at a non-native dimension is rejected here, at configuration time,
// with a clear provider-blind error, rather than accepted and then silently
// corrupted by slicing a vector space that has no valid lower-dimensional
// projection. Actually resizing the live rag_chunks column and re-embedding
// existing documents is done by the control-plane provisioning + re-embed
// routines; this package only decides what is allowed and how it maps.
package embedmodel

import "fmt"

// pgvector dimension ceilings, verified against /pgvector/pgvector
// (_autodocs/types.md and README, "Half-Precision Indexing"):
//
//   - A vector column carries an HNSW or IVFFlat index up to 2000 dimensions.
//   - A halfvec column (2-byte elements) halves the index tuple, raising the
//     practical HNSW ceiling to ~4000 dimensions via halfvec_cosine_ops.
//   - Both vector and halfvec store up to 16000 dimensions, but above ~4000
//     no ANN index of any kind is possible; queries fall back to a brute-force
//     sequential scan (small-corpus / demo only).
const (
	// HNSWVectorMaxDim is the largest dimension a vector column can be HNSW-
	// (or IVFFlat-) indexed at.
	HNSWVectorMaxDim = 2000
	// HalfvecIndexMaxDim is the largest dimension a halfvec column can be
	// HNSW-indexed at.
	HalfvecIndexMaxDim = 4000
	// VectorStorageMaxDim is the largest dimension pgvector will store in a
	// vector or halfvec column, indexed or not.
	VectorStorageMaxDim = 16000
)

// Model describes one embedding model this build knows natively.
type Model struct {
	// NativeDim is the vector width the model actually returns.
	NativeDim int
	// MRL reports whether the model was trained with Matryoshka
	// Representation Learning, so that its leading N dimensions are a valid
	// lower-dimensional embedding. Only an MRL model may be served at a
	// dimension below its native width; a non-MRL model must use its native
	// dimension.
	MRL bool
	// LiteLLMRoute is the model_name this model is served under in
	// deploy/litellm/config.yaml, when routed through LiteLLM. Empty for a
	// model only ever called directly (e.g. bge-m3 on a bare Ollama host).
	LiteLLMRoute string
}

// Registry lists every embedding model this build has verified dimension
// facts for, keyed by canonical model id. EMBEDDING_MODEL may be set to
// either a canonical id here or to one of the LiteLLMRoute values below;
// Lookup resolves both forms.
var Registry = map[string]Model{
	// bge-m3: multilingual (incl. Bangla), MRL-trained, native 1024-dim.
	// Enterprise/Ollama default (#295); not wired through LiteLLM in the
	// serverless demo today, so no LiteLLMRoute.
	"bge-m3": {NativeDim: 1024, MRL: true},

	// NVIDIA Nemotron-Embed VL 1B: OpenRouter free-pool primary for the
	// serverless demo, native 4096-dim. NOT documented as MRL, so it is not
	// selectable for indexed RAG at any reduced dimension (4096 exceeds the
	// halfvec index ceiling and it cannot be MRL-reduced) -- brute-force only.
	"nemotron-embed-vl-1b": {NativeDim: 4096, MRL: false, LiteLLMRoute: "route-openrouter-embedding"},

	// Qwen3 Embedding 8B: OpenRouter paid fallback for the serverless demo,
	// MRL-trained, native 4096-dim. Serviceable at an MRL-reduced supported
	// dimension (e.g. 1024 -> vector(1024) HNSW, the clean demo target).
	"qwen3-embedding-8b": {NativeDim: 4096, MRL: true, LiteLLMRoute: "route-openrouter-embedding-fallback"},
}

// Lookup finds a Model by canonical id or by LiteLLMRoute alias. ok is false
// for a model this build has no dimension facts for; callers treat that as
// "nothing model-specific to validate," not as an error — an admin may point
// at a custom model the registry does not know yet. The pgvector mapping
// (ResolvePgvector) is model-independent and still applies.
func Lookup(modelOrRoute string) (Model, bool) {
	if m, ok := Registry[modelOrRoute]; ok {
		return m, true
	}
	for _, m := range Registry {
		if m.LiteLLMRoute != "" && m.LiteLLMRoute == modelOrRoute {
			return m, true
		}
	}
	return Model{}, false
}

// Canonical maps a model id or LiteLLM route alias to its canonical registry
// id, so the startup env/config reconcile compares like with like: a service
// configured with the route alias "route-openrouter-embedding-fallback" and a
// config row holding the canonical "qwen3-embedding-8b" refer to the same
// model and must not be treated as a mismatch. Unknown inputs are returned
// unchanged (a custom model the registry does not know is its own canonical
// form).
func Canonical(modelOrRoute string) string {
	if _, ok := Registry[modelOrRoute]; ok {
		return modelOrRoute
	}
	for id, m := range Registry {
		if m.LiteLLMRoute != "" && m.LiteLLMRoute == modelOrRoute {
			return id
		}
	}
	return modelOrRoute
}

// Plan is the fully resolved, provisionable embedding configuration for a
// chosen (model, dim). It is what both services resolve their env config into
// and what the control-plane provisioning routine builds the column + index
// from, so the query path, the ingest path, and the physical schema are all
// derived from one computation.
type Plan struct {
	Model      string
	Dim        int
	NativeDim  int  // 0 when the model is unknown to the registry
	MRL        bool // false when the model is unknown
	KnownModel bool
	// PgType is the pgvector column type: "vector" or "halfvec".
	PgType string
	// Opclass is the HNSW operator class for the index: "vector_cosine_ops",
	// "halfvec_cosine_ops", or "" when the dimension is not indexable.
	Opclass string
	// Indexable reports whether an ANN (HNSW) index can be built. When false
	// the column stores vectors but search is a brute-force sequential scan.
	Indexable bool
	// ReduceTo is the client-side MRL reduction target: dim when an MRL
	// model's chosen dim is below its native width, else 0 (the endpoint is
	// expected to return exactly dim natively). Preferring the endpoint's
	// native `dimensions=dim` is still correct: a no-op reduction of an
	// already-dim-wide vector leaves it unchanged.
	ReduceTo int
}

// ColumnType renders the SQL column type text, e.g. "vector(1024)" or
// "halfvec(3000)".
func (p Plan) ColumnType() string {
	return fmt.Sprintf("%s(%d)", p.PgType, p.Dim)
}

// Cast returns the pgvector cast suffix a query vector must carry to match a
// column of the given pgvector type: "::halfvec" for a halfvec column, else
// "::vector". pgvector's vector<->halfvec casts are assignment-only, so a
// query written as `halfvec_col <=> $1::vector` has no operator and errors at
// runtime; the query vector must be cast to the column's own type. pgType
// originates only from ResolvePgvector (an enum-constrained "vector"/"halfvec"),
// never user input, so the result is safe to interpolate into SQL. An unknown
// or empty pgType falls back to "::vector", the shipped default column type.
func Cast(pgType string) string {
	if pgType == "halfvec" {
		return "::halfvec"
	}
	return "::vector"
}

// ResolvePgvector maps a dimension to its pgvector storage type, HNSW
// operator class, and whether an ANN index is possible. It is model-
// independent: it only encodes pgvector's own limits.
//
//   - 1..2000       -> vector(N),  vector_cosine_ops,  indexable
//   - 2001..4000    -> halfvec(N), halfvec_cosine_ops, indexable
//   - 4001..16000   -> halfvec(N), (no index), brute-force ONLY, and only when
//     allowUnindexed is set; otherwise rejected with a clear error
//   - >16000        -> rejected (exceeds pgvector storage)
func ResolvePgvector(dim int, allowUnindexed bool) (pgType, opclass string, indexable bool, err error) {
	switch {
	case dim <= 0:
		return "", "", false, fmt.Errorf("embedmodel: dimension must be strictly positive, got %d", dim)
	case dim <= HNSWVectorMaxDim:
		return "vector", "vector_cosine_ops", true, nil
	case dim <= HalfvecIndexMaxDim:
		return "halfvec", "halfvec_cosine_ops", true, nil
	case dim <= VectorStorageMaxDim:
		if !allowUnindexed {
			return "", "", false, fmt.Errorf(
				"embedmodel: dimension %d exceeds the %d-dim ANN-index ceiling; no HNSW index is possible, set EMBEDDING_ALLOW_UNINDEXED=1 to store it as an unindexed brute-force column (small-corpus only)",
				dim, HalfvecIndexMaxDim)
		}
		return "halfvec", "", false, nil
	default:
		return "", "", false, fmt.Errorf(
			"embedmodel: dimension %d exceeds the pgvector storage ceiling of %d",
			dim, VectorStorageMaxDim)
	}
}

// Resolve validates a chosen (model, dim) against the selectable policy and
// returns the full provisioning Plan. It is the single source of truth both
// edge-api and control-plane resolve their embedding configuration through.
//
// Rejections (all at configuration time, provider-blind):
//
//   - dim <= 0.
//   - dim greater than the model's native width (cannot invent dimensions).
//   - dim below native on a NON-MRL model (naive truncation is banned).
//   - dim that maps to no indexable pgvector type unless allowUnindexed is set.
//
// An unknown model skips the model-specific checks (native width and MRL are
// unknown) but still resolves the pgvector mapping by dimension.
func Resolve(model string, dim int, allowUnindexed bool) (Plan, error) {
	if dim <= 0 {
		return Plan{}, fmt.Errorf("embedmodel: EMBEDDING_DIM must be strictly positive, got %d", dim)
	}

	p := Plan{Model: model, Dim: dim}
	if m, ok := Lookup(model); ok {
		p.KnownModel = true
		p.NativeDim = m.NativeDim
		p.MRL = m.MRL
		switch {
		case dim > m.NativeDim:
			return Plan{}, fmt.Errorf(
				"embedmodel: model %q is native %d-dim; it cannot produce a wider %d-dim vector, choose a dimension <= %d",
				model, m.NativeDim, dim, m.NativeDim)
		case dim < m.NativeDim:
			if !m.MRL {
				return Plan{}, fmt.Errorf(
					"embedmodel: model %q is not MRL-trained; it cannot be reduced to %d dimensions (native %d), choose its native dimension or an MRL-trained model",
					model, dim, m.NativeDim)
			}
			p.ReduceTo = dim // legitimate MRL reduction to a supported width
		default:
			p.ReduceTo = 0 // dim == native: endpoint returns the full width
		}
	}

	pgType, opclass, indexable, err := ResolvePgvector(dim, allowUnindexed)
	if err != nil {
		return Plan{}, err
	}
	p.PgType, p.Opclass, p.Indexable = pgType, opclass, indexable
	return p, nil
}

// Validate reports whether a chosen (model, dim) is a serviceable
// configuration, without returning the resolved Plan. It is a thin wrapper
// over Resolve for call sites that only need the yes/no verdict.
func Validate(model string, dim int, allowUnindexed bool) error {
	_, err := Resolve(model, dim, allowUnindexed)
	return err
}

// SameConfig reports whether two (model, dim) configurations refer to the same
// embedding space: same dimension and same canonical model (route aliases
// resolve to their canonical id first). Both edge-api and control-plane use it
// at startup to reconcile their resolved env config against the active
// rag_embedding_config row; a false result means the running config would
// query a column provisioned for a different space, so RAG is disabled
// (fail-closed) rather than serving cross-space results. This closes the
// cross-service inconsistency #368: the same pure comparison runs in both
// services, so they cannot disagree.
func SameConfig(aModel string, aDim int, bModel string, bDim int) bool {
	return aDim == bDim && Canonical(aModel) == Canonical(bModel)
}
