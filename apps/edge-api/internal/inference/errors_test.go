package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Attributability guard for issue #722. A model name Open WebUI's document RAG
// sent that the catalog does not expose came back as a bare "404 Not Found" in
// its logs, and that read as a routing bug: two investigations went looking for
// an unregistered /v1/embeddings route before either checked the model name.
// The route was fine; the name was not. The response body has to keep naming
// the model that failed to resolve, because that is the only place the gateway
// says which one it was.
func TestModelNotFoundErrorNamesTheUnresolvableModel(t *testing.T) {
	for _, model := range []string{"text-embedding-3-small", "sentence-transformers/all-MiniLM-L6-v2"} {
		w := httptest.NewRecorder()
		writeModelNotFoundError(w, model)

		if w.Code != http.StatusNotFound {
			t.Fatalf("model %q: expected 404, got %d", model, w.Code)
		}

		var resp struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("model %q: response is not JSON: %v", model, err)
		}
		if !strings.Contains(resp.Error.Message, model) {
			t.Fatalf("model %q: message does not name the model: %s", model, resp.Error.Message)
		}
		if resp.Error.Code != "model_not_found" {
			t.Fatalf("model %q: expected code model_not_found, got %q", model, resp.Error.Code)
		}
	}
}

// Same property on the entitlement refusal, which deliberately reuses the
// message so a caller cannot tell "unknown model" from "model hidden from your
// tenant", but must still name the model for the operator reading the reply.
func TestModelNotEntitledErrorNamesTheModelAndRefusesWith403(t *testing.T) {
	w := httptest.NewRecorder()
	writeModelNotEntitledError(w, "hive-embedding-default")

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "hive-embedding-default") {
		t.Fatalf("message does not name the model: %s", w.Body.String())
	}
}
