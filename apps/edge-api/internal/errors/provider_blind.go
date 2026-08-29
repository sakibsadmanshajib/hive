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
	// responseStatus is what the CUSTOMER sees. It starts equal to httpStatus,
	// the real upstream status, and only diverges for 402 below. httpStatus
	// itself stays untouched so the operator log two lines down keeps the real
	// value even after responseStatus is remapped.
	responseStatus := httpStatus

	switch httpStatus {
	case http.StatusPaymentRequired:
		// A 402 here is the upstream refusing for ITS OWN funding (our
		// provider account's balance with OpenRouter, DeepSeek, or whoever),
		// never the Hive customer's balance: CreateReservation already
		// verified the customer's own credit before dispatch ever reached
		// this point (D-034, fail closed). Relaying a literal 402 tells the
		// customer the opposite, that THEY must pay, which is exactly the
		// provider refusal being presented as a caller fault the way issue
		// #1411 names. Treated as the same "temporarily unavailable" verdict
		// a 503/504 already gets: it is an availability problem on Hive's
		// side, not a request the caller can fix.
		responseStatus = http.StatusServiceUnavailable
		code = "upstream_unavailable"
	case http.StatusTooManyRequests:
		errType = "rate_limit_error"
		code = "upstream_rate_limited"
	case http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		code = "upstream_unavailable"
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// The upstream refused the CALLER request, so the envelope has to say
		// that. api_error with upstream_error told an SDK the gateway broke,
		// and told a human debugging their own payload to go and look at model
		// status, which is the wrong place entirely (#1348).
		errType = "invalid_request_error"
		code = "invalid_request"
	}

	message := sanitizeProviderBlindMessage(alias, responseStatus, rawMessage)
	logProviderBlindUpstreamError(w, alias, httpStatus, rawMessage, message)
	WriteError(w, responseStatus, errType, message, &code)
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
	if providerBlindLooksLikeRoutingInternals(lowerRaw) || providerBlindLooksLikeRoutingInternals(lowerMessage) {
		// LiteLLM's own fallback-group bookkeeping (issue #965): "No fallback
		// model group found for original model_group=X. Fallbacks=[...].
		// Retried: N times." Carries no provider name, so the regexes above
		// never touch it, but it is still internal routing detail no
		// customer-facing error should carry. Collapse the whole message
		// rather than trying to scrub an open-ended, LiteLLM-versioned
		// bookkeeping format field by field.
		if providerBlindRequestShaped(httpStatus) {
			// Same collapse, different verdict. On a request-shaped status
			// the bookkeeping exists because every pool member refused the
			// CALLER request, not because the alias is unavailable, and
			// saying otherwise sends a customer debugging their own payload
			// to check model status instead (#1348).
			return fallbackProviderBlindMessage(resourceLabel, httpStatus)
		}
		return fmt.Sprintf("%s is not available.", resourceLabel)
	}
	if providerBlindLooksLikeAuthFailure(httpStatus, lowerRaw) || providerBlindLooksLikeAuthFailure(httpStatus, lowerMessage) {
		return fmt.Sprintf("%s request was rejected by the upstream provider.", resourceLabel)
	}
	if httpStatus == http.StatusTooManyRequests || strings.Contains(lowerMessage, "rate limit") || strings.Contains(lowerRaw, "rate limit") {
		return fmt.Sprintf("%s is temporarily rate limited.", resourceLabel)
	}
	// Default deny. Everything reaching this line is upstream-authored prose
	// that none of the rules above recognized, and the default for that is
	// COLLAPSE, not forward.
	//
	// This used to be the other way round: forward unless a rule matched.
	// That is a denylist, exactly as wide as its literals, and it leaked
	// every shape nobody had enumerated yet. Measured on the previous
	// revision of this file, all of these reached a customer word for word:
	// "Insufficient Balance" (DeepSeek's actual 402 body), "You have
	// exceeded your monthly spend limit", "Your account is past due. Update
	// your payment information to continue" (the denylist had "payment
	// method" and not "payment information"), "Free tier limit reached for
	// this organization" (which tells a customer that OUR account is on a
	// free tier), and "Your organization has run out of prepaid funds".
	// Every one of them is an upstream talking about the account WE hold
	// with it, and the answer to five more of them is not five more
	// literals (PR #1303 review).
	//
	// Same inversion packages/sanitize made for the response-frame path in
	// #1253, for the same reason: an unrecognized shape must fail to leak
	// rather than fail to be caught. The cost is a vaguer sentence on
	// shapes nobody enumerated, and the raw text is in the operator log
	// either way.
	if !providerBlindCustomerActionable(httpStatus, lowerMessage) {
		return fallbackProviderBlindMessage(resourceLabel, httpStatus)
	}

	return message
}

// UpstreamUnavailableMessage is the customer-facing sentence for an upstream
// failure whose own text can never be forwarded and where there is no HTTP
// status left to classify by at all, which is the case for a mid-stream SSE
// error frame: the 200 was committed before the failure existed. Exported for
// the SSE relays (chat dispatch, RAG chat) that build a replacement frame via
// sanitize.ReplaceErrorFrame.
//
// It is Hive-owned, names no provider, blames the customer for nothing, and
// promises nothing the gateway enforces: it does not say the model will be
// back, only that it cannot serve now and that another one may.
func UpstreamUnavailableMessage(alias string) string {
	return fmt.Sprintf(
		"%s is unavailable right now. Try another model, or send this message again later.",
		providerBlindResourceLabel(alias),
	)
}

// providerBlindActionableTokens is the allowlist: the vocabulary of an
// upstream error the CUSTOMER can actually do something about. Forwarding
// upstream wording has exactly one justification, which is that the customer
// can act on it; anything else is detail about our side of the relationship
// and belongs in the log instead.
//
// Deliberately narrow, and deliberately free of money, quota, plan, account
// and organization vocabulary: none of those are ever about the caller's own
// balance on this gateway. Hive's own credit refusals never pass through this
// function -- chat.writeInsufficientQuota, inference.refuseOnReservationFailure
// and authz write theirs directly -- so collapsing that vocabulary here cannot
// swallow a message about the customer's own balance.
var providerBlindActionableTokens = []string{
	// The prompt does not fit the model the customer picked.
	"context length", "context window", "maximum context", "context_length",
	"prompt is too long", "reduce the length", "too long",
	// The request itself is malformed, and the field named is the customer's.
	"invalid value", "invalid type", "invalid schema", "invalid format",
	"unsupported parameter", "unsupported value", "unknown parameter",
	"missing required parameter", "must be one of", "must be between",
	"is required", "cannot be empty", "required property", "parse error",
	// The upstream refused the CONTENT, which only the customer can change.
	"content filter", "content_filter", "content policy", "safety", "moderation", "flagged",
	// Attachment shapes the customer controls.
	"invalid image", "unsupported image", "image_url", "file type",
}

// providerBlindCustomerActionable reports whether a sanitized upstream message
// may be forwarded to the customer verbatim.
//
// Gated on status first. A 5xx is never the caller's doing, a 401/403 is our
// credential rather than theirs, and a 429 is a wait-and-retry verdict the
// rule above already phrases for us, so only the request-shaped 4xx statuses
// can carry a message worth forwarding at all.
func providerBlindCustomerActionable(httpStatus int, message string) bool {
	switch httpStatus {
	case http.StatusBadRequest, http.StatusNotFound,
		http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
	default:
		return false
	}
	// Veto first. An allowlist token licenses forwarding the WHOLE message,
	// so a single upstream sentence that carries both ("Organization org-12345
	// is over its quota, and this prompt also exceeds the maximum context
	// length") would forward the account detail along with the actionable
	// part. The veto is not the primary defence and is not a substitute for
	// the allowlist above -- it is second, and it only ever collapses more.
	for _, token := range providerBlindNeverForwardTokens {
		if strings.Contains(message, token) {
			return false
		}
	}
	for _, token := range providerBlindActionableTokens {
		if strings.Contains(message, token) {
			return true
		}
	}
	return false
}

// providerBlindNeverForwardTokens is vocabulary about OUR relationship with
// the upstream: its funding, our tier, our organization. None of it is ever
// the caller's to act on, so a message carrying any of it is collapsed even
// when it also matches an actionable token. Kept tight and specific for the
// usual substring reason.
var providerBlindNeverForwardTokens = []string{
	"quota", "billing", "credits", "payment", "insufficient",
	"balance", "spend limit", "past due", "free tier", "prepaid",
	"invoice", "subscription", "top up", "top-up", "upgrade your plan",
	"organization",
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
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		// Names the request, not the model. The generic "request failed" this
		// used to return reads as a fault on our side of the call for a status
		// that means the opposite (#1348). It stays vague about WHICH
		// parameter, because the upstream sentence that knew was collapsed for
		// the reasons above, and inventing a field name would be worse than
		// pointing at the payload.
		return fmt.Sprintf("Invalid request for %s. Check the request parameters.", resourceLabel)
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

func providerBlindLooksLikeRoutingInternals(message string) bool {
	// "retried: N times" deliberately excluded: it is ordinary English a
	// legitimate upstream message could use on its own, so alone it risked
	// collapsing safe error text (security review finding on this PR). The
	// three tokens below are specific to LiteLLM's own bookkeeping
	// vocabulary and already fully cover issue #965's reported leak shape
	// without it.
	for _, token := range []string{"fallback model group", "model_group=", "fallbacks=["} {
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

// providerBlindRequestShaped reports whether an upstream status means the
// CALLER request was refused rather than the model being unable to serve it.
//
// Only the two statuses that can only be about the request itself. A 404 is
// deliberately not here: an upstream that does not know the model IS an
// availability answer, and the existing "is not available" sentence is the
// right one for it. A 429 and every 5xx are not the caller doing at all.
func providerBlindRequestShaped(httpStatus int) bool {
	return httpStatus == http.StatusBadRequest || httpStatus == http.StatusUnprocessableEntity
}
