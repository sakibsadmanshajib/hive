package errors

import (
	"encoding/json"
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
	CodeServiceUnavailable   Code = "SERVICE_UNAVAILABLE"
	CodeInternal             Code = "INTERNAL"
)

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
