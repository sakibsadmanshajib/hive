// Package executor_test is a black-box test package (not executor itself),
// deliberately: it needs to import both executor and batchstore, and
// batchstore already imports executor (see local_executor_adapters.go), so
// a same-package test here would be a compile-time import cycle. This is
// the standard Go pattern for a cross-package integration test.
package executor_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/batchstore"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/batchstore/executor"
)

// TestDispatcher_RealClientTruncation_NeverSucceeds is the end-to-end half
// of issue #1255 finding #2's fix (PR #1253 review finding M2).
// local_inference_test.go (package batchstore) proves the real
// LiteLLMInferenceClient detects an oversized response in isolation.
// dispatcher_test.go's TestDispatcher_TruncatedResponseIsNeverASuccess
// (package executor) proves the dispatcher handles that shape correctly,
// but only via a hand-mirrored fake whose comment says exactly that: it
// mirrors the client's behavior rather than exercising it. Nothing wired
// the REAL client through the REAL Dispatcher against a real oversized HTTP
// response, so a future drift between what the client actually returns and
// what the fake claims it returns would go unnoticed by either test alone.
// This closes that gap.
func TestDispatcher_RealClientTruncation_NeverSucceeds(t *testing.T) {
	// Mirrors batchstore's unexported maxLocalInferenceResponseBytes (4MiB):
	// duplicated rather than imported, since it is unexported and this is a
	// different package by design (see the package doc above). Any drift
	// between the two is exactly what this end-to-end test exists to catch.
	const maxLocalInferenceResponseBytes = 4 * 1024 * 1024
	oversized := strings.Repeat("a", maxLocalInferenceResponseBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"chatcmpl-x","content":"` + oversized + `"}`))
	}))
	defer srv.Close()

	client := batchstore.NewLiteLLMInferenceClient(srv.URL, "test-key")
	disp, err := executor.NewDispatcher(executor.Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, client, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res := disp.Dispatch(context.Background(), executor.InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         []byte(`{"model":"customer-alias-1","messages":[{"role":"user","content":"hi"}]}`),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Output != nil {
		t.Fatalf("oversized real-client response recorded as success: %+v", res.Output)
	}
	if res.Error == nil {
		t.Fatalf("expected a failed line, got neither output nor error")
	}
	if res.Error.Error.Code != "response_too_large" {
		t.Fatalf("code=%q want response_too_large", res.Error.Error.Code)
	}
	if res.ConsumedCredits != 0 {
		t.Fatalf("credits=%d want 0 for a failed line", res.ConsumedCredits)
	}
}

// TestDispatcher_RealClientWithinCap_Succeeds is the green-path control:
// the real client through the real dispatcher, for a response comfortably
// under the cap, still settles as an ordinary sanitized success.
func TestDispatcher_RealClientWithinCap_Succeeds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"gen-x","model":"route-x","choices":[{"index":0,"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	client := batchstore.NewLiteLLMInferenceClient(srv.URL, "test-key")
	disp, err := executor.NewDispatcher(executor.Config{Concurrency: 1, MaxRetries: 1, LineTimeout: 5 * time.Second}, client, nil)
	if err != nil {
		t.Fatalf("new dispatcher: %v", err)
	}

	res := disp.Dispatch(context.Background(), executor.InputLine{
		CustomID:     "x",
		Method:       "POST",
		URL:          "/v1/chat/completions",
		Body:         []byte(`{"model":"customer-alias-1","messages":[{"role":"user","content":"hi"}]}`),
		Alias:        "customer-alias-1",
		LiteLLMModel: "openrouter/deepseek/deepseek-v4-pro-0813",
	})
	if res.Error != nil {
		t.Fatalf("unexpected failure: %+v", res.Error)
	}
	if res.Output == nil {
		t.Fatalf("expected a successful output")
	}
	if strings.Contains(string(res.Output.Response.Body), "gen-x") || strings.Contains(string(res.Output.Response.Body), "route-x") {
		t.Fatalf("real-client success still leaked upstream id/model: %s", res.Output.Response.Body)
	}
}
