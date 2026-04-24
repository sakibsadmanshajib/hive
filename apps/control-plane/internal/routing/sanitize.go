package routing

import "strings"

func SanitizeProviderMessage(alias string, raw string) string {
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
