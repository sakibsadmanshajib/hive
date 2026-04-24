package inference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddings_MissingModel(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"input":"hello"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "model") {
		t.Fatalf("expected error about 'model', got: %s", msg)
	}
}

func TestEmbeddings_MissingInput(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	body := `{"model":"hive-embedding-default"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}

	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "input") {
		t.Fatalf("expected error about 'input', got: %s", msg)
	}
}

func TestEmbeddings_DimensionsOnNonSupportingModel(t *testing.T) {
	h := NewHandler(&Orchestrator{})
	dims := 256
	body, _ := json.Marshal(map[string]any{
		"model":      "hive-embedding-default",
		"input":      "hello",
		"dimensions": dims,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for unsupported dimensions, got %d", w.Code)
	}

	var errResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &errResp)
	errObj, ok := errResp["error"].(map[string]any)
	if !ok {
		t.Fatal("expected error object in response")
	}
	code, _ := errObj["code"].(string)
	if code != "unsupported_parameter" {
		t.Fatalf("expected code 'unsupported_parameter', got: %s", code)
	}
}

func TestEmbeddings_DimensionsSupportedOnEmbedding3(t *testing.T) {
	// Models containing "embedding-3" should pass the dimensions capability check.
	// The request will fail downstream (no live orchestrator), but should NOT return
	// unsupported_parameter at the handler validation stage.
	h := NewHandler(&Orchestrator{})
	dims := 256
	body, _ := json.Marshal(map[string]any{
		"model":      "text-embedding-3-small",
		"input":      "hello",
		"dimensions": dims,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/embeddings", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should NOT return 400 unsupported_parameter — may fail with another error
	// (e.g. 401/500 from nil orchestrator components) but NOT the dimensions gate.
	if w.Code == http.StatusBadRequest {
		var errResp map[string]any
		json.Unmarshal(w.Body.Bytes(), &errResp)
		errObj, _ := errResp["error"].(map[string]any)
		code, _ := errObj["code"].(string)
		if code == "unsupported_parameter" {
			t.Fatal("text-embedding-3-small should pass dimensions capability check")
		}
	}
}

func TestNormalizeEmbeddings(t *testing.T) {
	input := `{
		"object":"list",
		"data":[
			{"object":"embedding","embedding":[0.1,0.2,0.3],"index":0}
		],
		"model":"route-openrouter-embedding-small",
		"usage":{"prompt_tokens":5,"total_tokens":5}
	}`

	normalized, usage, err := normalizeEmbeddings([]byte(input), "hive-embedding-default")
	if err != nil {
		t.Fatalf("normalizeEmbeddings failed: %v", err)
	}

	var resp EmbeddingsResponse
	json.Unmarshal(normalized, &resp)

	if resp.Model != "hive-embedding-default" {
		t.Fatalf("expected model 'hive-embedding-default', got '%s'", resp.Model)
	}
	if resp.Object != "list" {
		t.Fatalf("expected object 'list', got '%s'", resp.Object)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 embedding, got %d", len(resp.Data))
	}
	if resp.Data[0].Object != "embedding" {
		t.Fatalf("expected data[0].object 'embedding', got '%s'", resp.Data[0].Object)
	}
	if usage == nil {
		t.Fatal("expected usage to be non-nil")
	}
	if usage.PromptTokens != 5 {
		t.Fatalf("expected prompt_tokens 5, got %d", usage.PromptTokens)
	}
	if usage.TotalTokens != 5 {
		t.Fatalf("expected total_tokens 5, got %d", usage.TotalTokens)
	}
	if usage.CompletionTokens != 0 {
		t.Fatalf("expected completion_tokens 0, got %d", usage.CompletionTokens)
	}
}

func TestNormalizeEmbeddings_ModelAliasReplaced(t *testing.T) {
	input := `{
		"object":"list",
		"data":[{"object":"embedding","embedding":[0.5,0.6],"index":0}],
		"model":"route-litellm-internal-embed",
		"usage":{"prompt_tokens":3,"total_tokens":3}
	}`

	normalized, _, err := normalizeEmbeddings([]byte(input), "hive-fast-embed")
	if err != nil {
		t.Fatalf("normalizeEmbeddings failed: %v", err)
	}

	// Ensure provider route handle is not present in output.
	if strings.Contains(string(normalized), "route-litellm-internal-embed") {
		t.Fatal("normalized response should not contain provider route handle")
	}

	var resp EmbeddingsResponse
	json.Unmarshal(normalized, &resp)
	if resp.Model != "hive-fast-embed" {
		t.Fatalf("expected model 'hive-fast-embed', got '%s'", resp.Model)
	}
}

func TestSupportsDimensions(t *testing.T) {
	cases := []struct {
		model   string
		expect  bool
	}{
		{"text-embedding-3-small", true},
		{"text-embedding-3-large", true},
		{"hive-embedding-3-small", true},
		{"hive-embedding-default", false},
		{"ada-002", false},
		{"text-embedding-ada-002", false},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := supportsDimensions(tc.model)
			if got != tc.expect {
				t.Fatalf("supportsDimensions(%q) = %v, want %v", tc.model, got, tc.expect)
			}
		})
	}
}
