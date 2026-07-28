package anthropic_test

// Structural guard for the defect this package shipped: POST /v1/messages built
// its own dispatch to LiteLLM and never called routing.SelectRoute. Because a
// LiteLLM model name IS a route id (deploy/litellm/config.yaml defines
// route-openrouter-default, route-groq-fast and friends), that single shortcut
// bypassed three controls at once: per-tenant model entitlement, the API-key
// alias allowlist, and prepaid credit metering.
//
// Code review did not catch it for two releases, so make it structural: any
// edge-api package that dispatches inference to LiteLLM must also resolve the
// alias through SelectRoute. The check is package-scoped on purpose -- the
// transport (inference/litellm_client.go) legitimately holds no routing call,
// while the orchestrator beside it does.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	// liteLLMTargetRegex matches a reference to the LiteLLM proxy's address or
	// credential. It is a whitelist of the identifier shapes the codebase uses
	// for them, not a blacklist of one wrong expression, so renaming a variable
	// does not slip past the guard.
	liteLLMTargetRegex = regexp.MustCompile(`(?i)litellm(url|baseurl|base_url|base|key|masterkey|master_key|client)`)
	// liteLLMDispatchRegex matches actually sending a request somewhere.
	liteLLMDispatchRegex = regexp.MustCompile(`http\.NewRequest|http\.Post|httpClient\.Do|HTTP\.Do|\.Do\(`)
	// routeSelectionRegex matches resolving a client alias to a route.
	routeSelectionRegex = regexp.MustCompile(`SelectRoute`)
)

// dispatchesToLiteLLM reports whether a Go source file both names the LiteLLM
// proxy and issues an HTTP request, which together mean it dispatches inference.
func dispatchesToLiteLLM(source string) bool {
	return liteLLMTargetRegex.MatchString(source) && liteLLMDispatchRegex.MatchString(source)
}

// TestLiteLLMDispatchDetectorIsNotVacuous proves the detector below still fires
// on the exact code that caused the defect, and still ignores code that merely
// mentions the OpenAI chat path. Without this, a broken regex would turn the
// guard into a test that always passes.
func TestLiteLLMDispatchDetectorIsNotVacuous(t *testing.T) {
	offending := `
		upstream, err := http.NewRequestWithContext(r.Context(), http.MethodPost,
			strings.TrimRight(h.deps.LiteLLMURL, "/")+"/v1/chat/completions", bytes.NewReader(body))
		resp, err := h.deps.HTTP.Do(upstream)
	`
	if !dispatchesToLiteLLM(offending) {
		t.Fatal("detector no longer recognises a direct LiteLLM dispatch; the guard below is vacuous")
	}

	benign := `
		sub := r.Clone(r.Context())
		sub.URL.Path = "/v1/chat/completions"
		h.deps.OpenAIChat.ServeHTTP(translator, sub)
	`
	if dispatchesToLiteLLM(benign) {
		t.Fatal("detector flags in-process delegation to the OpenAI chat path")
	}
}

func TestNoLiteLLMDispatchWithoutRouteSelection(t *testing.T) {
	internalDir := filepath.Join(edgeAPIRepoRoot(t), "apps", "edge-api", "internal")

	packages := map[string][]string{}   // package dir -> files that dispatch
	hasRouteSelection := map[string]bool{} // package dir -> resolves aliases

	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		source := string(raw)
		pkgDir := filepath.Dir(path)
		if routeSelectionRegex.MatchString(source) {
			hasRouteSelection[pkgDir] = true
		}
		if dispatchesToLiteLLM(source) {
			packages[pkgDir] = append(packages[pkgDir], path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", internalDir, err)
	}
	if len(packages) == 0 {
		t.Fatal("no LiteLLM dispatch found anywhere in apps/edge-api/internal; the guard is scanning the wrong tree")
	}

	for pkgDir, files := range packages {
		if hasRouteSelection[pkgDir] {
			continue
		}
		t.Errorf(
			"package %s dispatches to LiteLLM but never calls SelectRoute (%s).\n"+
				"LiteLLM model names are route ids, so an unresolved dispatch lets a caller "+
				"address a route directly and skip per-tenant model entitlement, the API-key "+
				"alias allowlist, and credit metering. Resolve the client alias through "+
				"inference.RoutingClient.SelectRoute, or delegate to a handler that does.",
			relativeToRepo(t, pkgDir), strings.Join(relativeAll(t, files), ", "),
		)
	}
}

// The unsupported-endpoint middleware rejects any /v1/ path the support matrix
// does not mark supported_now, and it wraps the whole mux. /v1/messages was
// never registered, so every request to this surface was 404'd before it
// reached the handler at all. Keep it registered.
func TestMessagesEndpointsAreRegisteredInTheSupportMatrix(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(edgeAPIRepoRoot(t),
		"packages", "openai-contract", "matrix", "support-matrix.json"))
	if err != nil {
		t.Fatalf("read support matrix: %v", err)
	}

	var matrix struct {
		Endpoints []struct {
			Method string `json:"method"`
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatalf("decode support matrix: %v", err)
	}

	want := map[string]bool{
		"POST /v1/messages":              false,
		"POST /v1/messages/count_tokens": false,
	}
	for _, endpoint := range matrix.Endpoints {
		key := endpoint.Method + " " + endpoint.Path
		if _, tracked := want[key]; tracked && endpoint.Status == "supported_now" {
			want[key] = true
		}
	}
	for key, found := range want {
		if !found {
			t.Errorf("%s is not supported_now in the support matrix, so the "+
				"unsupported-endpoint middleware will 404 it before the handler runs", key)
		}
	}
}

// edgeAPIRepoRoot walks up from the test's working directory to the repository
// root (the directory holding go.work).
func edgeAPIRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root (go.work) not found above the test working directory")
		}
		dir = parent
	}
}

func relativeToRepo(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(edgeAPIRepoRoot(t), path)
	if err != nil {
		return path
	}
	return rel
}

func relativeAll(t *testing.T, paths []string) []string {
	t.Helper()
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, relativeToRepo(t, path))
	}
	return out
}
