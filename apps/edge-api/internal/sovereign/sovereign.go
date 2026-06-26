// Package sovereign enforces data-sovereignty startup constraints.
// When HIVE_SOVEREIGN is set to a truthy value, external LLM provider
// keys must not be configured.
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

// isSovereignEnabled returns true for any case-insensitive, trimmed truthy value:
// "1", "true", "yes", "on". Everything else (including empty) is false.
func isSovereignEnabled(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Check returns an error if sovereign mode is active and any external provider key is set.
// All violating keys are collected and reported together so the operator can fix them
// in one pass. Pass os.Getenv as the lookup in production; pass a map lookup in tests.
func Check(getenv func(string) string) error {
	if !isSovereignEnabled(getenv("HIVE_SOVEREIGN")) {
		return nil
	}
	var violations []string
	for _, key := range externalProviderKeys {
		if strings.TrimSpace(getenv(key)) != "" {
			violations = append(violations, key)
		}
	}
	if len(violations) == 0 {
		return nil
	}
	return fmt.Errorf(
		"FATAL: HIVE_SOVEREIGN=true but the following external LLM provider keys are set: %s. "+
			"External LLM providers are prohibited in sovereign mode. "+
			"Unset all listed keys and restart.",
		strings.Join(violations, ", "),
	)
}
