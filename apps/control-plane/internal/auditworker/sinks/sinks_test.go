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

	s := sinks.NewELK(sinks.ELKConfig{URL: srv.URL + "/hive-audit/_doc", APIKey: "k"})
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

	s := sinks.NewDatadog(sinks.DatadogConfig{URL: srv.URL + "/api/v2/logs", APIKey: "dd"})
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

func TestLangfuseSkipsNonLLMActions(t *testing.T) {
	called := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	s := sinks.NewLangfuse(sinks.LangfuseConfig{Host: srv.URL, PublicKey: "p", SecretKey: "s"})
	require.NoError(t, s.Send(context.Background(), map[string]any{"action": "AUTH_SIGNIN_SUCCESS"}))
	require.Equal(t, 0, called)
	require.NoError(t, s.Send(context.Background(), map[string]any{
		"action":     "CHAT_REQUEST",
		"after_json": map[string]any{"model": "gpt-4o-mini"},
	}))
	require.Equal(t, 1, called)
}
