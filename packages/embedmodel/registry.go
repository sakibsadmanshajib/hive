// Package embedmodel is the single source of truth for which embedding
// models Hive knows about, how wide a vector each one natively produces, and
// which LiteLLM route (if any) serves it. Both apps/edge-api and
// apps/control-plane import this instead of keeping their own copy, so the
// bge-m3/nemotron/qwen3 dimension facts cannot drift between the query path
// and the ingest path.
//
// This is the foundation piece of the admin-controlled embedding redesign:
// it lets EMBEDDING_MODEL / EMBEDDING_DIM / EMBEDDING_TRUNCATE_TO be
// validated for mutual consistency at process startup instead of only
// discovering a mismatch when a vector of the wrong width hits Postgres.
// Choosing a model, resizing the live rag_chunks column, and re-embedding
// existing documents onto a new model are later PRs; this package only
// answers "are these three env vars consistent with each other."
package embedmodel

import "fmt"

// Model describes one embedding model this build knows natively.
type Model struct {
	// NativeDim is the vector width the model actually returns.
	NativeDim int
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
	"bge-m3": {NativeDim: 1024},

	// NVIDIA Nemotron-Embed VL 1B: OpenRouter free-pool primary for the
	// serverless demo, native 4096-dim.
	"nemotron-embed-vl-1b": {NativeDim: 4096, LiteLLMRoute: "route-openrouter-embedding"},

	// Qwen3 Embedding 8B: OpenRouter paid fallback for the serverless demo,
	// MRL-trained, native 4096-dim. Today's demo default (docker-compose.yml).
	"qwen3-embedding-8b": {NativeDim: 4096, LiteLLMRoute: "route-openrouter-embedding-fallback"},
}

// Lookup finds a Model by canonical id or by LiteLLMRoute alias. ok is false
// for a model this build has no dimension facts for; callers treat that as
// "nothing to validate against," not as an error — an admin may point at a
// custom model the registry does not know yet.
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

// Validate checks that model, dim (EMBEDDING_DIM), and truncateTo
// (EMBEDDING_TRUNCATE_TO) are mutually consistent:
//
//   - model unknown to Registry: nothing to check, returns nil.
//   - native dim == dim: truncateTo must be 0 (no truncation needed).
//   - native dim >  dim: truncateTo must equal dim (MRL truncation required).
//   - native dim <  dim: always an error; a narrower vector cannot be padded
//     out to a wider column.
func Validate(model string, dim, truncateTo int) error {
	if dim <= 0 {
		return fmt.Errorf("embedmodel: EMBEDDING_DIM must be strictly positive, got %d", dim)
	}
	if truncateTo < 0 {
		return fmt.Errorf("embedmodel: EMBEDDING_TRUNCATE_TO cannot be negative, got %d", truncateTo)
	}
	m, ok := Lookup(model)
	if !ok {
		return nil
	}
	switch {
	case m.NativeDim == dim:
		if truncateTo != 0 {
			return fmt.Errorf("embedmodel: model %q is native %d-dim, matching EMBEDDING_DIM=%d; EMBEDDING_TRUNCATE_TO must be 0, got %d",
				model, m.NativeDim, dim, truncateTo)
		}
	case m.NativeDim > dim:
		if truncateTo != dim {
			return fmt.Errorf("embedmodel: model %q is native %d-dim, wider than EMBEDDING_DIM=%d; EMBEDDING_TRUNCATE_TO must equal %d, got %d",
				model, m.NativeDim, dim, dim, truncateTo)
		}
	default:
		return fmt.Errorf("embedmodel: model %q is native %d-dim, narrower than EMBEDDING_DIM=%d; cannot pad a narrower vector, pick a wider model or lower EMBEDDING_DIM",
			model, m.NativeDim, dim)
	}
	return nil
}
