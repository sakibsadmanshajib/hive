package routing

import (
	"strings"
	"testing"
)

func TestSanitizeProviderMessageRemovesProviderNames(t *testing.T) {
	raw := "openrouter route-openrouter-default failed after openrouter/auto retried groq and route-groq-fast before openrouter/free"

	sanitized := SanitizeProviderMessage("", raw)

	for _, forbidden := range []string{"openrouter", "groq", "route-openrouter-default", "openrouter/auto"} {
		if strings.Contains(sanitized, forbidden) {
			t.Fatalf("expected sanitized message to remove %q, got %q", forbidden, sanitized)
		}
	}
}

func TestSanitizeProviderMessageUsesAliasWhenAvailable(t *testing.T) {
	raw := "route-openrouter-default failed while openrouter/auto retried route-groq-fast"

	sanitized := SanitizeProviderMessage("hive-fast", raw)

	if !strings.Contains(sanitized, "hive-fast") {
		t.Fatalf("expected sanitized message to mention alias, got %q", sanitized)
	}
	for _, forbidden := range []string{"openrouter", "groq", "route-openrouter-default", "openrouter/auto"} {
		if strings.Contains(sanitized, forbidden) {
			t.Fatalf("expected sanitized message to remove %q, got %q", forbidden, sanitized)
		}
	}
}
