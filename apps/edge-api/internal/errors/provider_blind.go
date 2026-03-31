package errors

import (
	"net/http"
	"strings"
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

	message := sanitizeProviderBlindMessage(alias, rawMessage)
	WriteError(w, httpStatus, errType, message, &code)
}

func sanitizeProviderBlindMessage(alias string, raw string) string {
	resourceReplacement := "requested model"
	providerReplacement := "upstream provider"
	if trimmedAlias := strings.TrimSpace(alias); trimmedAlias != "" {
		resourceReplacement = trimmedAlias
		providerReplacement = trimmedAlias
	}

	message := strings.NewReplacer(
		"route-openrouter-default", resourceReplacement,
		"route-openrouter-auto", resourceReplacement,
		"route-groq-fast", resourceReplacement,
		"openrouter/auto", resourceReplacement,
		"openrouter/free", resourceReplacement,
		"openrouter", providerReplacement,
		"groq", providerReplacement,
	).Replace(raw)

	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		return resourceReplacement
	}

	return message
}
