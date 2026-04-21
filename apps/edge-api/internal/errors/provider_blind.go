package errors

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
)

var (
	providerWordPattern = strings.Join([]string{
		"anthropic",
		"azure",
		"bedrock",
		"cerebras",
		"cohere",
		"deepseek",
		"fireworks",
		"gemini",
		"google",
		"groq",
		"litellm",
		"mistral",
		"openai",
		"openrouter",
		"perplexity",
		"together",
		"vertex",
		"xai",
	}, "|")
	providerNameRegex      = regexp.MustCompile(`(?i)\b(?:` + providerWordPattern + `)\b`)
	providerModelRegex     = regexp.MustCompile(`(?i)\b(?:` + providerWordPattern + `)(?:/[^\s"'(),:;]+)+\b`)
	routeSlugRegex         = regexp.MustCompile(`(?i)\broute-[a-z0-9][a-z0-9._/-]*\b`)
	liteLLMClassRegex      = regexp.MustCompile(`(?i)\blitellm\.[a-z0-9_.-]*(?:error|exception)\b`)
	providerExceptionRegex = regexp.MustCompile(`(?i)\b(?:` + providerWordPattern + `)[a-z0-9_.-]*(?:error|exception)\b`)
	camelCaseErrorRegex    = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9_]*(?:Error|Exception)\b`)
)

func WriteProviderBlindUpstreamError(w http.ResponseWriter, alias string, httpStatus int, rawMessage string) {
	errType := "api_error"
	code := "upstream_error"

	switch httpStatus {
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
		code = "upstream_rate_limited"
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = "upstream_unavailable"
	}

	message := sanitizeProviderBlindMessage(alias, httpStatus, rawMessage)
	logProviderBlindUpstreamError(w, alias, httpStatus, rawMessage, message)
	WriteError(w, httpStatus, errType, message, &code)
}

func sanitizeProviderBlindMessage(alias string, httpStatus int, raw string) string {
	resourceLabel := providerBlindResourceLabel(alias)
	message := extractProviderBlindMessage(raw)
	if message == "" {
		return fallbackProviderBlindMessage(resourceLabel, httpStatus)
	}

	message = strings.TrimSpace(message)
	message = providerModelRegex.ReplaceAllString(message, resourceLabel)
	message = routeSlugRegex.ReplaceAllString(message, resourceLabel)
	message = liteLLMClassRegex.ReplaceAllString(message, "upstream error")
	message = providerExceptionRegex.ReplaceAllString(message, "upstream error")
	message = camelCaseErrorRegex.ReplaceAllString(message, "upstream error")
	message = providerNameRegex.ReplaceAllString(message, "upstream provider")
	message = normalizeProviderBlindWhitespace(strings.Trim(message, `"'`))
	message = strings.Trim(message, " :-;,")
	message = strings.ReplaceAll(message, "upstream error: upstream error", "upstream error")
	message = strings.ReplaceAll(message, "upstream error upstream error", "upstream error")
	message = normalizeProviderBlindWhitespace(message)

	lowerMessage := strings.ToLower(message)
	lowerRaw := strings.ToLower(strings.TrimSpace(raw))
	if providerBlindLooksLikeTransportFailure(lowerRaw) || providerBlindLooksLikeTransportFailure(lowerMessage) {
		return fmt.Sprintf("%s is temporarily unavailable.", resourceLabel)
	}
	if providerBlindLooksLikeAuthFailure(httpStatus, lowerRaw) || providerBlindLooksLikeAuthFailure(httpStatus, lowerMessage) {
		return fmt.Sprintf("%s request was rejected by the upstream provider.", resourceLabel)
	}
	if httpStatus == http.StatusTooManyRequests || strings.Contains(lowerMessage, "rate limit") || strings.Contains(lowerRaw, "rate limit") {
		return fmt.Sprintf("%s is temporarily rate limited.", resourceLabel)
	}
	if message == "" || lowerMessage == resourceLabel || lowerMessage == "upstream error" || lowerMessage == "upstream provider" {
		return fallbackProviderBlindMessage(resourceLabel, httpStatus)
	}

	return message
}

func providerBlindResourceLabel(alias string) string {
	trimmedAlias := normalizeProviderBlindWhitespace(strings.TrimSpace(alias))
	if trimmedAlias == "" {
		return "requested model"
	}
	if routeSlugRegex.MatchString(trimmedAlias) || providerNameRegex.MatchString(trimmedAlias) || strings.Contains(trimmedAlias, "/") {
		return "requested model"
	}
	return trimmedAlias
}

func extractProviderBlindMessage(raw string) string {
	trimmedRaw := strings.TrimSpace(raw)
	if trimmedRaw == "" {
		return ""
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmedRaw), &decoded); err != nil {
		return trimmedRaw
	}

	if extracted := extractProviderBlindValue(decoded, 0); extracted != "" {
		return extracted
	}
	return trimmedRaw
}

func extractProviderBlindValue(value any, depth int) string {
	if depth > 8 {
		return ""
	}

	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return ""
		}
		if nested := extractProviderBlindMessage(trimmed); nested != "" && nested != trimmed {
			return nested
		}
		return trimmed
	case []any:
		for _, item := range typed {
			if extracted := extractProviderBlindValue(item, depth+1); extracted != "" {
				return extracted
			}
		}
	case map[string]any:
		for _, key := range []string{"error", "message", "detail", "details", "error_description", "title", "reason"} {
			if candidate, ok := typed[key]; ok {
				if extracted := extractProviderBlindValue(candidate, depth+1); extracted != "" {
					return extracted
				}
			}
		}
		for _, candidate := range typed {
			if extracted := extractProviderBlindValue(candidate, depth+1); extracted != "" {
				return extracted
			}
		}
	}

	return ""
}

func fallbackProviderBlindMessage(resourceLabel string, httpStatus int) string {
	switch httpStatus {
	case http.StatusTooManyRequests:
		return fmt.Sprintf("%s is temporarily rate limited.", resourceLabel)
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return fmt.Sprintf("%s is temporarily unavailable.", resourceLabel)
	default:
		return fmt.Sprintf("%s request failed.", resourceLabel)
	}
}

func normalizeProviderBlindWhitespace(message string) string {
	return strings.Join(strings.Fields(message), " ")
}

func logProviderBlindUpstreamError(w http.ResponseWriter, alias string, httpStatus int, rawMessage string, clientMessage string) {
	requestID := strings.TrimSpace(w.Header().Get("x-request-id"))
	log.Printf(
		`provider_blind_upstream_error request_id=%q alias=%q status=%d raw_message=%q client_message=%q`,
		requestID,
		strings.TrimSpace(alias),
		httpStatus,
		strings.TrimSpace(rawMessage),
		clientMessage,
	)
}

func providerBlindLooksLikeTransportFailure(message string) bool {
	for _, token := range []string{
		"dial tcp",
		"connection refused",
		"connect:",
		"context deadline exceeded",
		"timeout",
		"temporarily unavailable",
		"no such host",
		"econnrefused",
	} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

func providerBlindLooksLikeAuthFailure(httpStatus int, message string) bool {
	if httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden {
		return true
	}
	for _, token := range []string{"authentication", "unauthorized", "forbidden", "rejected"} {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}
