package sinks

import (
	"context"
	"errors"
	"net/http"
	"os"
)

type LangfuseConfig struct {
	Host      string
	PublicKey string
	SecretKey string
	HTTP      *http.Client
}

type Langfuse struct {
	cfg LangfuseConfig
}

func NewLangfuse(cfg LangfuseConfig) *Langfuse {
	cfg.HTTP = httpClient(cfg.HTTP)
	return &Langfuse{cfg: cfg}
}

func (s *Langfuse) Name() string { return "langfuse" }

func (s *Langfuse) Send(ctx context.Context, row map[string]any) error {
	action, _ := row["action"].(string)
	if action != "CHAT_REQUEST" {
		return nil
	}
	if s.cfg.Host == "" {
		return errors.New("langfuse: host required")
	}

	after, _ := row["after_json"].(map[string]any)
	// Allowlist non-PII metadata fields. The previous code forwarded
	// `after` as-is which leaked prompt/completion content into
	// Langfuse even when LANGFUSE_INCLUDE_CONTENT was unset. Mirror
	// the Sentry sink's allowlist approach: pass only fields that are
	// dimensions/costs/IDs, never user-generated text.
	allowedMetadataKeys := []string{
		"model", "provider", "in_tokens", "out_tokens",
		"latency_ms", "cost_credits", "finish_reason", "request_id",
		"retrieval_doc_ids", "stream",
	}
	safeMetadata := make(map[string]any, len(allowedMetadataKeys))
	for _, k := range allowedMetadataKeys {
		if v, ok := after[k]; ok {
			safeMetadata[k] = v
		}
	}
	generationBody := map[string]any{
		"name":      "chat.completions",
		"model":     after["model"],
		"metadata":  safeMetadata,
		"traceId":   row["request_id"],
		"startTime": row["ts"],
	}
	if os.Getenv("LANGFUSE_INCLUDE_CONTENT") == "true" {
		generationBody["input"] = after["prompt"]
		generationBody["output"] = after["completion"]
	}

	body := map[string]any{
		"batch": []map[string]any{
			{"type": "trace-create", "body": map[string]any{"id": row["request_id"], "name": "CHAT_REQUEST"}},
			{"type": "span-create", "body": map[string]any{"traceId": row["request_id"], "name": "edge-api"}},
			{"type": "generation-create", "body": generationBody},
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Host+"/api/public/ingestion", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.cfg.PublicKey, s.cfg.SecretKey)
	return postJSON(ctx, s.cfg.HTTP, req.URL.String(), body, map[string]string{
		"Authorization": req.Header.Get("Authorization"),
	})
}
