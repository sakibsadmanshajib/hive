package litellmconfig_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Guards over the keyless-provider path added for OpenCode Zen
// (https://opencode.ai/zen/v1), the first upstream in this catalog that takes
// no credential at all.
//
// Two independent things have to hold for that upstream to serve, and both are
// silent when broken, which is why they are pinned here rather than left to a
// live call nobody runs:
//
//  1. The generator must not emit an `os.environ/` reference for a provider
//     whose api_key_env is empty. `os.environ/` with nothing after it is not a
//     reference LiteLLM can resolve; it yields no key, the OpenAI client is
//     then constructed with none, and the deployment fails at request time
//     with an error about a missing api_key that names no route.
//  2. The access headers this upstream gates on must survive a catalog sync.
//     They live in deploy/litellm/config.yaml because the generator owns only
//     model, api_base and api_key; every other litellm_params key is merged
//     from the file field by field (mergeParams). If that merge dropped them,
//     the route would go on looking correct in the repository and answer 429
//     to every request on the box.
//
// Measured against the pinned image (ghcr.io/berriai/litellm:v1.98.0) and the
// real upstream on 2026-08-30: with the two extra_headers below the gateway
// returns HTTP 200; with only the User-Agent it returns 401 (the literal
// api_key reaches the upstream as a bearer token and is rejected); with
// neither it returns 429 (the OpenAI client's own User-Agent is refused).

const noCredentialPlaceholder = "keyless"

func keylessEntry() litellmconfig.ModelEntry {
	return litellmconfig.ModelEntry{
		ModelName:   "route-free-opencode-zen",
		LiteLLMName: "openai/big-pickle",
		APIBase:     "https://opencode.ai/zen/v1",
		APIKeyEnv:   "",
	}
}

func firstEntryParams(t *testing.T, data []byte, modelName string) map[string]interface{} {
	t.Helper()

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &parsed))

	list, ok := parsed["model_list"].([]interface{})
	require.True(t, ok, "model_list must be a sequence")

	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if entry["model_name"] != modelName {
			continue
		}
		params, ok := entry["litellm_params"].(map[string]interface{})
		require.True(t, ok, "litellm_params must be a mapping for %s", modelName)
		return params
	}

	t.Fatalf("no model_list entry named %s", modelName)
	return nil
}

// TestGenerateKeylessProviderEmitsNoDanglingEnvironmentReference is the whole
// reason custom_providers can express a keyless provider at all. An empty
// api_key_env must produce a literal, never "os.environ/" with an empty
// variable name.
func TestGenerateKeylessProviderEmitsNoDanglingEnvironmentReference(t *testing.T) {
	out, err := litellmconfig.Generate(litellmconfig.Config{
		Models:          []litellmconfig.ModelEntry{keylessEntry()},
		GeneralSettings: litellmconfig.GeneralSettings{MasterKey: "test-master-key"},
	})
	require.NoError(t, err)

	params := firstEntryParams(t, out, "route-free-opencode-zen")

	assert.Equal(t, noCredentialPlaceholder, params["api_key"],
		"a provider with an empty api_key_env must get a literal placeholder; os.environ/ with no variable name resolves to nothing and the deployment fails at request time")
	assert.Equal(t, "https://opencode.ai/zen/v1", params["api_base"],
		"api_base still has to be emitted: the generic openai/ adapter without one would send this request to api.openai.com")
}

// TestGenerateKeyedProviderStillUsesEnvironmentReference is the other half of
// the same branch. Nothing about the keyless path may weaken the ordinary one,
// where the key must stay an environment reference and never a literal in a
// file that is written to disk and read in review.
func TestGenerateKeyedProviderStillUsesEnvironmentReference(t *testing.T) {
	out, err := litellmconfig.Generate(litellmconfig.Config{
		Models:          twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{MasterKey: "test-master-key"},
	})
	require.NoError(t, err)

	params := firstEntryParams(t, out, "gpt-4o")
	assert.Equal(t, "os.environ/OPENROUTER_API_KEY", params["api_key"])
}

// TestWriteAndRestartPreservesKeylessAccessHeaders is the access mechanism
// itself. The User-Agent is the entire gate on this upstream and the empty
// Authorization is what stops the literal api_key being sent as a bearer
// token, so a sync that dropped either would take the route from serving to
// refusing without changing a single line of the repository.
func TestWriteAndRestartPreservesKeylessAccessHeaders(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	existing := `
general_settings:
  master_key: old-key
model_list:
  - model_name: route-free-opencode-zen
    litellm_params:
      model: openai/big-pickle
      api_base: https://opencode.ai/zen/v1
      api_key: keyless
      extra_headers:
        User-Agent: opencode
        Authorization: ""
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, litellmconfig.Config{
		Models:             []litellmconfig.ModelEntry{keylessEntry()},
		GeneralSettings:    litellmconfig.GeneralSettings{MasterKey: "new-key"},
		KnownRouteIDs:      []string{"route-free-opencode-zen"},
		KnownGroupNames:    []string{"route-free-opencode-zen"},
		ExistingConfigPath: configPath,
	}, &mockRestarter{}))

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)

	params := firstEntryParams(t, data, "route-free-opencode-zen")

	headers, ok := params["extra_headers"].(map[string]interface{})
	require.True(t, ok, "extra_headers must survive the sync; without them this upstream answers 429 to every request")
	assert.Equal(t, "opencode", headers["User-Agent"],
		"User-Agent: opencode is the entire access gate on this upstream (measured 2026-08-30: any other value returns 429 FreeUsageLimitError)")

	auth, present := headers["Authorization"]
	require.True(t, present, "the Authorization override must survive; without it the literal api_key reaches the upstream as a bearer token and is rejected 401")
	assert.Equal(t, "", auth, "the override must be empty: measured 2026-08-30, any non-empty bearer token returns 401 Invalid API key")

	// The generator still owns its three keys on the same entry.
	assert.Equal(t, noCredentialPlaceholder, params["api_key"])
	assert.Equal(t, "openai/big-pickle", params["model"])
}

// TestSyncOverTheRealSeedConfigKeepsTheKeylessRouteServable closes the loop the
// two tests above only cover in halves. It runs the real generator over the
// repository's actual deploy/litellm/config.yaml with the entry the database
// row produces, which is exactly what control-plane does on every catalog sync,
// and asserts the surviving entry is one this upstream will answer.
//
// A hand-written fixture cannot catch the failure that matters here: the seed
// entry being absent, misnamed or missing a header. This reads the file that
// actually ships.
func TestSyncOverTheRealSeedConfigKeepsTheKeylessRouteServable(t *testing.T) {
	seed, err := os.ReadFile(filepath.Join(repoRoot(t), "deploy", "litellm", "config.yaml"))
	require.NoError(t, err)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, seed, 0o600))

	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, litellmconfig.Config{
		Models:             []litellmconfig.ModelEntry{keylessEntry()},
		GeneralSettings:    litellmconfig.GeneralSettings{MasterKey: "sync-key"},
		KnownRouteIDs:      []string{"route-free-opencode-zen"},
		KnownGroupNames:    []string{"route-free-opencode-zen"},
		ExistingConfigPath: configPath,
	}, &mockRestarter{}))

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)

	params := firstEntryParams(t, data, "route-free-opencode-zen")

	headers, ok := params["extra_headers"].(map[string]interface{})
	require.True(t, ok, "the shipped seed config must carry extra_headers on this route, or a sync produces an entry that answers 429 to everything")
	assert.Equal(t, "opencode", headers["User-Agent"])
	assert.Equal(t, "", headers["Authorization"])
	assert.Equal(t, noCredentialPlaceholder, params["api_key"])
	assert.Equal(t, "https://opencode.ai/zen/v1", params["api_base"])
}

// repoRoot walks up from the test's working directory to the repository root,
// identified by go.work. The routing package has its own copy for the same
// reason; duplicating six lines beats exporting a test helper across packages.
func repoRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.work found above the working directory; cannot locate the repository root")
		}
		dir = parent
	}
}
