// Package sovereign enforces data-sovereignty startup constraints.
// When HIVE_SOVEREIGN=true, external LLM provider keys must not be configured.
package sovereign

import (
	"fmt"
	"strings"
)

// externalProviderKeys lists every env var that routes traffic to an external LLM provider.
var externalProviderKeys = []string{
	"OPENROUTER_API_KEY",
	"GROQ_API_KEY",
}

// Check returns an error if sovereign mode is active and any external provider key is set.
// Pass os.Getenv as the lookup in production; pass a map lookup in tests.
func Check(getenv func(string) string) error {
	if strings.TrimSpace(getenv("HIVE_SOVEREIGN")) != "true" {
		return nil
	}
	for _, key := range externalProviderKeys {
		if strings.TrimSpace(getenv(key)) != "" {
			return fmt.Errorf(
				"FATAL: HIVE_SOVEREIGN=true but %s is set. "+
					"External LLM providers are prohibited in sovereign mode. "+
					"Unset the key and restart.",
				key,
			)
		}
	}
	return nil
}
