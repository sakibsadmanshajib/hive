package routing

import "strings"

func SanitizeProviderMessage(alias string, raw string) string {
	resourceReplacement := "requested model"
	providerReplacement := "upstream provider"
	if trimmedAlias := strings.TrimSpace(alias); trimmedAlias != "" {
		resourceReplacement = trimmedAlias
		providerReplacement = trimmedAlias
	}

	// Route ids come first and map to the resource name, because they are the
	// most specific patterns here. Without an explicit entry a route id still
	// gets scrubbed by the bare "groq" and "openrouter" tokens below, but only
	// mid-string, leaving mangled output like "route-upstream provider-small"
	// where the customer should simply see their own alias. Routes carrying
	// neither token, such as the DeepSeek ones, would not be rewritten at all
	// and would leak an internal route id verbatim. Every route seeded by
	// supabase/migrations/20260822_02_catalog_alias_restructure.sql is listed.
	message := strings.NewReplacer(
		"route-openrouter-default", resourceReplacement,
		"route-openrouter-auto", resourceReplacement,
		"route-groq-fast", resourceReplacement,
		"route-groq-small", resourceReplacement,
		"route-groq-medium", resourceReplacement,
		"route-groq-default", resourceReplacement,
		"route-groq-auto", resourceReplacement,
		"route-deepseek-v4-flash", resourceReplacement,
		"route-deepseek-v4-pro", resourceReplacement,
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
