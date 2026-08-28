package chat_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/chat"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/stretchr/testify/require"
)

// chatDispatchBodyOfSize returns a valid session chat-dispatch body padded
// to exactly n bytes via a trailing "_pad" string field. Built by direct
// string concatenation, not json.Marshal, for byte-exact control at the
// MaxRequestBodyBytes-1 / MaxRequestBodyBytes+1 boundary (issue #1250).
func chatDispatchBodyOfSize(t *testing.T, n int) string {
	t.Helper()
	const prefix = `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}],"_pad":"`
	const suffix = `"}`
	padLen := n - len(prefix) - len(suffix)
	require.GreaterOrEqual(t, padLen, 0, "target size too small for prefix/suffix overhead")
	return prefix + strings.Repeat("x", padLen) + suffix
}

func newAuthedChatRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{
		ID:       uuid.New(),
		TenantID: uuid.New(),
		Role:     "member",
		Email:    "test@example.com",
	}))
	return req
}

// TestDispatchBodyOneByteUnderLimitIsAccepted proves a body one byte under
// the unified 10 MiB cap parses and reaches route selection, rather than
// being rejected as malformed.
func TestDispatchBodyOneByteUnderLimitIsAccepted(t *testing.T) {
	handler := chat.NewDispatch(chat.Deps{
		Routing:    newPassthroughRoutingClient(t),
		LiteLLMURL: "http://unused",
		DeploySHA:  "s",
		Env:        "test",
	})
	req := newAuthedChatRequest(t, chatDispatchBodyOfSize(t, apierr.MaxRequestBodyBytes-1))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Accounting/Billing are deliberately unwired here: the handler fails
	// closed with a clean 503 once it reaches startSettlement (billing.go's
	// Accounting==nil guard), which proves the body parsed and passed route
	// selection without needing a live DB pool for this boundary test.
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.NotContains(t, rec.Body.String(), "bad json")
}

// TestDispatchBodyOneByteOverLimitIsHonest413 is the core regression: before
// the fix, io.LimitReader truncated a body one byte over the cap silently,
// the truncated bytes failed json.Unmarshal, and the caller saw a lying
// "bad json" with no mention of size (issue #1250).
func TestDispatchBodyOneByteOverLimitIsHonest413(t *testing.T) {
	handler := chat.NewDispatch(chat.Deps{
		LiteLLMURL: "http://unused",
		DeploySHA:  "s",
		Env:        "test",
	})
	req := newAuthedChatRequest(t, chatDispatchBodyOfSize(t, apierr.MaxRequestBodyBytes+1))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())

	var errBody struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(strings.NewReader(rec.Body.String())).Decode(&errBody))
	require.Equal(t, "REQUEST_TOO_LARGE", errBody.Error.Code)
	require.Contains(t, errBody.Error.Message, "MiB")
	require.NotContains(t, strings.ToLower(errBody.Error.Message), "json")
}

// TestDispatchContentLengthOverLimit_RejectedWithoutReading proves the
// declared-oversize fast path fires before any body bytes are read: the
// actual body here is small, only the Content-Length header lies.
func TestDispatchContentLengthOverLimit_RejectedWithoutReading(t *testing.T) {
	handler := chat.NewDispatch(chat.Deps{
		LiteLLMURL: "http://unused",
		DeploySHA:  "s",
		Env:        "test",
	})
	req := newAuthedChatRequest(t, `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"hi"}]}`)
	req.ContentLength = apierr.MaxRequestBodyBytes + 1
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code, rec.Body.String())
}

// TestDispatchTrustedBody_SkipsTheCap proves apierr.WithTrustedBody (set by
// the /v1/messages surface on its translated sub-request when it delegates
// to the session chat-dispatch path, PR #1273 review finding 2) makes this
// handler skip MaxRequestBodyBytes entirely: a body over the cap still
// reaches route selection instead of being refused as too large.
func TestDispatchTrustedBody_SkipsTheCap(t *testing.T) {
	handler := chat.NewDispatch(chat.Deps{
		Routing:    newPassthroughRoutingClient(t),
		LiteLLMURL: "http://unused",
		DeploySHA:  "s",
		Env:        "test",
	})
	req := newAuthedChatRequest(t, chatDispatchBodyOfSize(t, apierr.MaxRequestBodyBytes+1024))
	req = req.WithContext(apierr.WithTrustedBody(req.Context()))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	// Same fail-closed 503 as TestDispatchBodyOneByteUnderLimitIsAccepted
	// (Accounting/Billing deliberately unwired), which proves the body was
	// read in full and reached route selection, not refused as too large.
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
}
