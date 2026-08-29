package errors_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
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

// TestMaxRequestBodyBytesIsTenMiB pins the exact value issue #1250 settled
// on. A PR #1273 review mutation test changed this constant to 100 MiB and
// every package that consumes it still passed, because nothing asserted the
// specific number -- only that requests near whatever the constant said got
// accepted or rejected. This closes that gap directly.
func TestMaxRequestBodyBytesIsTenMiB(t *testing.T) {
	require.Equal(t, 10<<20, errors.MaxRequestBodyBytes)
}

// TestStableType_RequestEntityTooLarge pins stableType's 413 case (added
// alongside MaxRequestBodyBytes so a too-large body's wire "type" field
// reads REQUEST_TOO_LARGE rather than defaulting to INTERNAL). A PR #1273
// review mutation test deleted this case and every package still passed,
// because no test asserted the "type" field for a 413 Write -- callers only
// ever checked "code" and the HTTP status. This closes that gap directly,
// and TestWriteShapeAndType above already covers a status this case must
// NOT match (403 -> FORBIDDEN), so the two together bound the branch.
func TestStableType_RequestEntityTooLarge(t *testing.T) {
	rec := httptest.NewRecorder()

	errors.Write(rec, http.StatusRequestEntityTooLarge, errors.CodeRequestTooLarge, "too large")

	var got struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "REQUEST_TOO_LARGE", got.Error.Code)
	require.Equal(t, "REQUEST_TOO_LARGE", got.Error.Type)
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
