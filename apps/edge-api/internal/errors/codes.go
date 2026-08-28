package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

type Code string

const (
	CodeUnauthenticated      Code = "UNAUTHENTICATED"
	CodeJWTExpired           Code = "JWT_EXPIRED"
	CodeNoTenant             Code = "NO_TENANT"
	CodeForbidden            Code = "FORBIDDEN"
	CodeCrossTenant          Code = "CROSS_TENANT"
	CodeInvalidTenantSetting Code = "INVALID_TENANT_SETTING"
	CodeInvalidRequest       Code = "INVALID_REQUEST"
	CodeRequestTooLarge      Code = "REQUEST_TOO_LARGE"
	CodeServiceUnavailable   Code = "SERVICE_UNAVAILABLE"
	CodeInternal             Code = "INTERNAL"
)

// MaxRequestBodyBytes is the single request-body-size cap for every JSON
// endpoint edge-api parses directly from a client: /v1/messages,
// /v1/chat/completions, /v1/completions, /v1/embeddings, /v1/responses, and
// the internal session chat-dispatch path.
//
// Value: 10 MiB, matching the pre-existing OpenAI-shaped endpoints' limit
// (the Go-rewrite baseline, #89). /v1/messages and the internal
// chat-dispatch path previously capped at 4 MiB, introduced independently
// and later (#168/#243) with no comment or test tying it to the existing
// convention (issue #1250). Lowering the established 10 MiB endpoints would
// break any caller already sending a body between 4 and 10 MiB; raising the
// narrower ones to match is a pure widening and cannot break an existing
// caller. One constant, used everywhere it applies: the next accidental
// divergence has to edit this comment to happen.
const MaxRequestBodyBytes = 10 << 20 // 10 MiB

// RequestTooLargeMessage is the standard, limit-naming message every
// body-size refusal uses, so the number in the client-facing text can never
// drift from MaxRequestBodyBytes.
func RequestTooLargeMessage() string {
	return fmt.Sprintf("Request body exceeds the maximum allowed size of %d MiB.", MaxRequestBodyBytes/(1<<20))
}

var stableErrorLeakPatterns = []*regexp.Regexp{
	// Provider names — extended to cover Google/Gemini/Mistral/Cohere/
	// Cerebras/DeepSeek/xAI which OpenRouter routes through.
	regexp.MustCompile(`(?i)\b(openai|anthropic|openrouter|groq|ollama|vllm|sglang|nim|aura|litellm|google|gemini|vertex(?:[-_]?ai)?|mistral|cohere|cerebras|deepseek|xai|together|fireworks|replicate|perplexity)\b`),
	regexp.MustCompile(`https?://[^\s"'<>]+`),
	regexp.MustCompile(`/v[0-9]+/[^\s"'<>]+`),
	// Currency / cost leak — prefix ($1.23) AND postfix (1.23 USD).
	// The original regex only matched the prefix form so OpenRouter
	// 402 errors with the postfix shape (e.g. "costs 0.002 USD")
	// slipped past untouched.
	regexp.MustCompile(`\$\d+(?:\.\d+)?`),
	regexp.MustCompile(`(?i)\b\d+(?:\.\d+)?\s*(USD|EUR|GBP|JPY|CNY|INR|AUD|CAD|BDT|SGD|HKD|KRW|TRY)\b`),
	regexp.MustCompile(`(?i)\b(upstream|provider|backend)\b`),
}

func sanitiseStableMessage(message string) string {
	for _, pattern := range stableErrorLeakPatterns {
		message = pattern.ReplaceAllString(message, "[redacted]")
	}
	return message
}

func stableType(status int) string {
	switch {
	case status == http.StatusUnauthorized:
		return "UNAUTHORIZED"
	case status == http.StatusForbidden:
		return "FORBIDDEN"
	case status == http.StatusBadRequest:
		return "INVALID_REQUEST"
	case status == http.StatusRequestEntityTooLarge:
		return "REQUEST_TOO_LARGE"
	case status == http.StatusServiceUnavailable:
		return "SERVICE_UNAVAILABLE"
	case status >= http.StatusInternalServerError:
		return "INTERNAL"
	default:
		return "INTERNAL"
	}
}

func Write(w http.ResponseWriter, status int, code Code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":       string(code),
			"message":    sanitiseStableMessage(message),
			"request_id": uuid.NewString(),
			"type":       stableType(status),
		},
	})
}
