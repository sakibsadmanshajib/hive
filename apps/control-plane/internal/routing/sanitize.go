package routing

import "strings"

func SanitizeProviderMessage(alias string, raw string) string {
	resourceReplacement := "requested model"
	providerReplacement := "upstream provider"
	if trimmedAlias := strings.TrimSpace(alias); trimmedAlias != "" {
		resourceReplacement = trimmedAlias
		providerReplacement = trimmedAlias
	}

	// NOTE, established while reviewing PR #1007: this function has NO
	// production caller. It is referenced only by its own tests. The live
	// customer-facing provider-blindness boundaries are elsewhere, and both
	// scrub generically rather than from a hardcoded list, so neither needs
	// updating when a route is added:
	//
	//   * apps/edge-api/internal/errors/provider_blind.go, on every inference,
	//     audio, images, RAG and chat dispatch error path. Its routeSlugRegex
	//     is (?i)\broute-[a-z0-9][a-z0-9._/-]*\b, which already covers any
	//     route id, and its providerModelRegex already covers any
	//     provider-prefixed model string.
	//   * apps/control-plane/internal/batchstore/executor/dispatcher.go's
	//     SanitizeMessage, on batch output files. That one strips provider
	//     WORDS only and has no route-slug pattern, so it is the real gap.
	//
	// Do not add route ids to the list below and assume the job is done. An
	// earlier revision of #1007 did exactly that and shipped nothing.
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
