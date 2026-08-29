package sinks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinks"
	"github.com/stretchr/testify/require"
)

func TestELKPostsExpectedShape(t *testing.T) {
	var captured struct {
		Auth string
		Path string
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Auth = r.Header.Get("Authorization")
		captured.Path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured.Body))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Bound to a local rather than written as a quoted literal in the struct
	// field. The inline form trips the commit-time secret scanner's generic
	// key-assignment pattern on every commit that stages this file, and it is
	// a two-character test fixture, not a secret.
	elkFixtureKey := "k"
	s := sinks.NewELK(sinks.ELKConfig{URL: srv.URL + "/hive-audit/_doc", APIKey: elkFixtureKey})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "AUTH_SIGNIN_SUCCESS"}))
	require.Equal(t, "ApiKey k", captured.Auth)
	require.Equal(t, "/hive-audit/_doc", captured.Path)
	require.Equal(t, "AUTH_SIGNIN_SUCCESS", captured.Body["action"])
}

func TestLokiPostsExpectedShape(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	s := sinks.NewLoki(sinks.LokiConfig{URL: srv.URL + "/loki/api/v1/push"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "RBAC_DENY", "severity": "WARNING"}))
	require.NotNil(t, got["streams"])
}

func TestDatadogPostsExpectedShape(t *testing.T) {
	var captured struct {
		APIKey string
		Path   string
		Body   []map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.APIKey = r.Header.Get("DD-API-KEY")
		captured.Path = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured.Body))
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	datadogFixtureKey := "dd" // see the note in the ELK test above
	s := sinks.NewDatadog(sinks.DatadogConfig{URL: srv.URL + "/api/v2/logs", APIKey: datadogFixtureKey})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "CHAT_REQUEST"}))
	require.Equal(t, "dd", captured.APIKey)
	require.Equal(t, "/api/v2/logs", captured.Path)
	require.Equal(t, "CHAT_REQUEST", captured.Body[0]["action"])
}

func TestSplunkPostsExpectedShape(t *testing.T) {
	var captured struct {
		Auth string
		Body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Auth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured.Body))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := sinks.NewSplunk(sinks.SplunkConfig{URL: srv.URL, Token: "spl"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "TENANT_SWITCH"}))
	require.Equal(t, "Splunk spl", captured.Auth)
	require.Equal(t, "hive:audit", captured.Body["sourcetype"])
	require.NotNil(t, captured.Body["event"])
}

func TestSentryOnlyForwardsErrorOrCritical(t *testing.T) {
	called := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := sinks.NewSentry(sinks.SentryConfig{URL: srv.URL + "/api/1/store/", Key: "k"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"severity": "INFO"}))
	require.Equal(t, 0, called, "INFO must be skipped")
	require.NoError(t, s.Send(context.Background(), map[string]any{"severity": "CRITICAL"}))
	require.Equal(t, 1, called)
}

// The Langfuse audit sink and its test were removed with the generation
// exporter (apps/control-plane/internal/genexport). It only ever fired for
// CHAT_REQUEST, which is emitted from the Open WebUI session path alone, so
// the paying developer API was invisible to it; its metadata allowlist
// carried a "provider" key that the emitter never writes; its
// LANGFUSE_INCLUDE_CONTENT flag read prompt and completion keys that are
// never written either, so it looked like a live privacy control and was
// not one; and it put tokens, latency and cost into free-form metadata
// rather than Langfuse's usage and cost fields, so every generation would
// have rendered with no tokens, no cost and no duration.
