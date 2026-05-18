package errors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/stretchr/testify/require"
)

func TestWriteShapeAndType(t *testing.T) {
	rec := httptest.NewRecorder()

	errors.Write(rec, http.StatusForbidden, errors.CodeCrossTenant, "no")

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))

	var got struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			Type      string `json:"type"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "CROSS_TENANT", got.Error.Code)
	require.Equal(t, "FORBIDDEN", got.Error.Type)
	require.NotEmpty(t, got.Error.RequestID)
}

func TestWriteSanitisesProviderLeakInMessage(t *testing.T) {
	rec := httptest.NewRecorder()

	errors.Write(
		rec,
		http.StatusServiceUnavailable,
		errors.CodeServiceUnavailable,
		"upstream openai/v1/chat/completions returned rate-limit at $0.0024 per 1k tokens",
	)

	var got struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotContains(t, got.Error.Message, "openai")
	require.NotContains(t, got.Error.Message, "$0.0024")
	require.NotContains(t, got.Error.Message, "/v1/chat/completions")
}
