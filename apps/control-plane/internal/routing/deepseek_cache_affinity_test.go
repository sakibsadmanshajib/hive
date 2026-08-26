package routing

import (
	"strings"
	"testing"
)

// Offline guards over the DeepSeek prompt-cache endpoint-affinity fix
// (fix/cache-realization, 2026-08-26).
//
// Live evidence behind these assertions (OpenRouter direct probes against
// the demo box key, 2026-08-26, identical ~975-token prompts):
//
//   - UNPINNED: three calls to ~deepseek/deepseek-v4-flash-latest landed on
//     three different providers (Inceptron, Fireworks, OpenInference) with
//     cached_tokens=0 every time; three calls to deepseek/deepseek-v4-pro-0813
//     landed on DigitalOcean, SiliconFlow and Together, also all zero. A cold
//     endpoint cannot serve a cache hit, so with per-call endpoint churn the
//     advertised supports_cache_read=true on these routes never pays off --
//     the parity report finding this fix closes (/tmp/reports/parity-inhive.md,
//     supplement B3).
//   - PINNED via OpenRouter's soft provider.order preference (fallbacks left
//     enabled): flash stuck to Incepton at cached_tokens=512 and pro stuck to
//     DigitalOcean at cached_tokens=768 on every repeat call. Endpoint-level
//     caching works; per-call endpoint selection was the defect.
//
// These tests are positional guards over deploy/litellm/config.yaml in the
// style of sqlparse_test.go: if someone drops or renames the extra_body block
// again, the routes go back to silent per-call endpoint churn, the catalog's
// cache-read pricing becomes dead config, and nothing else in CI would notice.

const litellmConfigRelPath = "deploy/litellm/config.yaml"

func litellmConfigYAML(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, litellmConfigRelPath)
}

// litellmEntryBody returns the YAML text of one model_list entry, from its
// `- model_name:` line up to (excluding) the next `- model_name:` line.
func litellmEntryBody(t *testing.T, modelName string) string {
	t.Helper()
	lines := strings.Split(litellmConfigYAML(t), "\n")
	start := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "- model_name: "+modelName {
			start = i
			break
		}
	}
	if start == -1 {
		t.Fatalf("%s has no model_list entry %s", litellmConfigRelPath, modelName)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "- model_name: ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// TestDeepSeekRoutesCarryCacheEndpointAffinity pins the soft provider.order
// preference on both paid DeepSeek chat routes. Without it OpenRouter may
// route consecutive identical prompts to different endpoints, each with a
// cold prefix cache, and the saving supports_cache_read=true advertises
// never materializes for any caller.
func TestDeepSeekRoutesCarryCacheEndpointAffinity(t *testing.T) {
	cases := []struct {
		modelName     string
		pinnedPartner string
	}{
		{modelName: "route-deepseek-v4-flash", pinnedPartner: "Inceptron"},
		{modelName: "route-deepseek-v4-pro", pinnedPartner: "DigitalOcean"},
	}
	for _, tc := range cases {
		t.Run(tc.modelName, func(t *testing.T) {
			body := litellmEntryBody(t, tc.modelName)

			if !strings.Contains(body, "extra_body:") {
				t.Fatalf("route %s carries no extra_body block; prompt-cache endpoint affinity is gone and supports_cache_read=true is once again an empty promise (%s)", tc.modelName, litellmConfigRelPath)
			}
			if !strings.Contains(body, "provider:") {
				t.Fatalf("route %s extra_body carries no provider block", tc.modelName)
			}
			if !strings.Contains(body, "order:") {
				t.Fatalf("route %s extra_body.provider carries no order preference; per-call endpoint churn returns", tc.modelName)
			}
			if !strings.Contains(body, tc.pinnedPartner) {
				t.Fatalf("route %s order preference lost its pinned partner %q; update both this test and the config together if the partner changed deliberately", tc.modelName, tc.pinnedPartner)
			}
			// Deliberately NOT allow_fallbacks:false: the order entry is a soft
			// preference, so a dead pinned endpoint falls back instead of taking
			// the paid alias down. Assert the cliff was not added by mistake.
			if strings.Contains(body, "allow_fallbacks") {
				t.Fatalf("route %s must keep fallbacks allowed; hard-pinning a paid flagship route to one endpoint trades availability for cache hits", tc.modelName)
			}
		})
	}
}
