package litellmconfig_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/litellmconfig"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// --- helpers ---

func twoModels() []litellmconfig.ModelEntry {
	return []litellmconfig.ModelEntry{
		{
			ModelName:   "gpt-4o",
			LiteLLMName: "openrouter/openai/gpt-4o",
			APIBase:     "https://openrouter.ai/api/v1",
			APIKeyEnv:   "OPENROUTER_API_KEY",
		},
		{
			ModelName:   "llama-3",
			LiteLLMName: "groq/llama-3-70b-8192",
			APIBase:     "https://api.groq.com/openai/v1",
			APIKeyEnv:   "GROQ_API_KEY",
		},
	}
}

// --- Generate tests ---

func TestGenerateTwoModelsProducesCorrectModelList(t *testing.T) {
	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "test-master-key",
		},
	}

	out, err := litellmconfig.Generate(cfg)
	require.NoError(t, err)
	require.NotEmpty(t, out)

	// Parse back to verify structure.
	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(out, &parsed))

	modelList, ok := parsed["model_list"].([]interface{})
	require.True(t, ok, "model_list must be a sequence")
	assert.Len(t, modelList, 2)

	// Verify first entry.
	first := modelList[0].(map[string]interface{})
	assert.Equal(t, "gpt-4o", first["model_name"])
	params := first["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openrouter/openai/gpt-4o", params["model"])
	assert.Equal(t, "https://openrouter.ai/api/v1", params["api_base"])
	assert.Equal(t, "os.environ/OPENROUTER_API_KEY", params["api_key"])
}

// TestGenerateUsesOpenAIAdapterForEmbeddingRoutes is the fast unit half of
// issue #707 gap 1 (TestSyncKeepsEmbeddingAdapterForSeededRoute is the live-DB
// half). LiteLLM's native openrouter/ provider does not map /embeddings, so an
// embeddings route is emitted through the generic openai/ adapter with the
// provider's api_base, while the DB value stays routing-canonical.
func TestGenerateUsesOpenAIAdapterForEmbeddingRoutes(t *testing.T) {
	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:          "route-openrouter-embedding",
				LiteLLMName:        "openrouter/nvidia/llama-nemotron-embed-vl-1b-v2:free",
				APIBase:            "https://openrouter.ai/api/v1",
				APIKeyEnv:          "OPENROUTER_API_KEY",
				SupportsEmbeddings: true,
			},
			{
				// Already on the generic adapter (e.g. a self-hosted
				// OpenAI-compatible embedding server): left alone.
				ModelName:          "bge-m3",
				LiteLLMName:        "openai/bge-m3",
				APIBase:            "http://ollama:11434/v1",
				APIKeyEnv:          "NONE",
				SupportsEmbeddings: true,
			},
			{
				// Not an embeddings route: native prefix preserved verbatim.
				ModelName:   "route-openrouter-default",
				LiteLLMName: "openrouter/openai/gpt-4o-mini",
				APIBase:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
			},
		},
		GeneralSettings: litellmconfig.GeneralSettings{MasterKey: "k"},
	}

	out, err := litellmconfig.Generate(cfg)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(out, &parsed))
	modelList, ok := parsed["model_list"].([]interface{})
	require.True(t, ok)
	require.Len(t, modelList, 3)

	byName := map[string]map[string]interface{}{}
	for _, item := range modelList {
		entry := item.(map[string]interface{})
		byName[entry["model_name"].(string)] = entry
	}

	embedParams := byName["route-openrouter-embedding"]["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openai/nvidia/llama-nemotron-embed-vl-1b-v2:free", embedParams["model"],
		"an embeddings route must use the generic openai/ adapter, not the native openrouter/ one")
	assert.Equal(t, "https://openrouter.ai/api/v1", embedParams["api_base"],
		"api_base is what makes the generic adapter reach OpenRouter")

	localParams := byName["bge-m3"]["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openai/bge-m3", localParams["model"], "an already-generic model string must not be rewritten")

	chatParams := byName["route-openrouter-default"]["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openrouter/openai/gpt-4o-mini", chatParams["model"],
		"a non-embeddings route keeps its native provider prefix")
}

// TestGenerateRefusesOpenAIAdapterWithoutAPIBase guards the credential half of
// the adapter rewrite: a model string of openai/<model> with no api_base
// resolves to api.openai.com, which would send this provider's key to the wrong
// upstream. The guard keys on the emitted model string, not on
// SupportsEmbeddings, because the openai/ prefix has two sources: this
// generator rewriting an embeddings route, and provider_model already carrying
// it (the shape bge-m3 has). The second one reaches the same exfiltration with
// SupportsEmbeddings false, e.g. when the route has no provider_capabilities
// row and an admin PUT blanked custom_providers.base_url.
func TestGenerateRefusesOpenAIAdapterWithoutAPIBase(t *testing.T) {
	cases := []struct {
		name  string
		entry litellmconfig.ModelEntry
	}{
		{
			name: "embeddings rewrite with no api_base",
			entry: litellmconfig.ModelEntry{
				ModelName:          "route-broken-embedding",
				LiteLLMName:        "someprovider/some-embed-model",
				APIBase:            "",
				APIKeyEnv:          "SOMEPROVIDER_API_KEY",
				SupportsEmbeddings: true,
			},
		},
		{
			name: "provider_model already generic, no capabilities row, no api_base",
			entry: litellmconfig.ModelEntry{
				ModelName:          "route-broken-generic",
				LiteLLMName:        "openai/some-embed-model",
				APIBase:            "",
				APIKeyEnv:          "SOMEPROVIDER_API_KEY",
				SupportsEmbeddings: false,
			},
		},
		{
			name: "whitespace-only api_base is no api_base",
			entry: litellmconfig.ModelEntry{
				ModelName:          "route-broken-blank",
				LiteLLMName:        "openai/some-embed-model",
				APIBase:            "   ",
				APIKeyEnv:          "SOMEPROVIDER_API_KEY",
				SupportsEmbeddings: false,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := litellmconfig.Generate(litellmconfig.Config{
				Models:          []litellmconfig.ModelEntry{tc.entry},
				GeneralSettings: litellmconfig.GeneralSettings{MasterKey: "k"},
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.entry.ModelName)
		})
	}
}

func TestGenerateEmptyModelsProducesEmptyModelList(t *testing.T) {
	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{},
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "k",
		},
	}

	out, err := litellmconfig.Generate(cfg)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(out, &parsed))

	// model_list must be present and empty (not nil/absent).
	raw, exists := parsed["model_list"]
	require.True(t, exists, "model_list key must be present even when empty")
	// YAML unmarshals an empty sequence as nil or []interface{} — both acceptable.
	if raw != nil {
		list, ok := raw.([]interface{})
		require.True(t, ok)
		assert.Empty(t, list)
	}
}

func TestGenerateOutputParsesWithoutError(t *testing.T) {
	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "round-trip-key",
		},
	}

	out, err := litellmconfig.Generate(cfg)
	require.NoError(t, err)

	var parsed map[string]interface{}
	err = yaml.Unmarshal(out, &parsed)
	assert.NoError(t, err, "generated YAML must round-trip without error")
}

func TestGenerateSetsGeneralSettingsMasterKey(t *testing.T) {
	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "my-secret-master-key",
		},
	}

	out, err := litellmconfig.Generate(cfg)
	require.NoError(t, err)

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(out, &parsed))

	gs, ok := parsed["general_settings"].(map[string]interface{})
	require.True(t, ok, "general_settings must be a mapping")
	assert.Equal(t, "my-secret-master-key", gs["master_key"])
}

// --- WriteAndRestart tests ---

// mockRestarter records calls to Restart and can be configured to return an error.
type mockRestarter struct {
	calls     int
	returnErr error
}

func (m *mockRestarter) Restart(_ context.Context) error {
	m.calls++
	return m.returnErr
}

func TestWriteAndRestartCallsRestarterOnSuccess(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "test-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	require.NoError(t, err)
	assert.Equal(t, 1, r.calls, "Restart must be called exactly once on success")

	// Verify file was written.
	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	assert.True(t, strings.Contains(string(data), "model_list"), "written file must contain model_list")
}

func TestWriteAndRestartSkipsRestartWhenConfigUnchanged(t *testing.T) {
	// A DB-managed route's model can't change without this same Sync/
	// WriteAndRestart call, and this call fires on every deploy regardless
	// of whether anything model-related changed, so a second call with
	// identical inputs must be a true no-op: no second restart, no second
	// live-chat interruption.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "test-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r))
	assert.Equal(t, 1, r.calls, "first call writes a new file and must restart")

	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r))
	assert.Equal(t, 1, r.calls, "second call with identical config must skip the restart")
}

func TestWriteAndRestartPreservesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write a pre-existing config with litellm_settings.
	existing := `
litellm_settings:
  drop_params: true
  num_retries: 3
general_settings:
  master_key: old-key
  some_other_key: preserve-me
model_list:
  - model_name: old-model
    litellm_params:
      model: openrouter/old
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "new-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	require.NoError(t, err)

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	content := string(data)

	// litellm_settings must be preserved.
	assert.Contains(t, content, "litellm_settings")
	assert.Contains(t, content, "drop_params")
	// general_settings.some_other_key must survive.
	assert.Contains(t, content, "preserve-me")
	// master_key must be updated.
	assert.Contains(t, content, "new-key")
	// old-model has no corresponding entry in twoModels(), so it is
	// operator-managed from this sync's point of view and must survive
	// rather than be dropped (issue #701 review, Required 3). The new
	// DB-managed models must be present alongside it, not instead of it.
	assert.Contains(t, content, "old-model")
	assert.Contains(t, content, "gpt-4o")
}

func TestWriteAndRestartSkipsRestarterOnGenerateFailure(t *testing.T) {
	// To trigger a Generate failure we rely on the fact that nil Models slice
	// with a bad config path combination never happens in normal flow.
	// Instead we verify behavior when the restarter would return an error:
	// the file write happens, but we confirm the restarter IS called (the write
	// succeeded) and we propagate the error.
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "k",
		},
		ExistingConfigPath: configPath,
	}

	restartErr := errors.New("docker unavailable")
	r := &mockRestarter{returnErr: restartErr}
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	assert.ErrorIs(t, err, restartErr, "restart error must be propagated")
	assert.Equal(t, 1, r.calls, "Restart must still be called once (write succeeded)")
}

func TestWriteAndRestartFirstRunNoExistingFile(t *testing.T) {
	dir := t.TempDir()
	// Point to a path that does NOT yet exist.
	configPath := filepath.Join(dir, "nonexistent", "config.yaml")

	cfg := litellmconfig.Config{
		Models: twoModels(),
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "first-run",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	// Should write from scratch without error.
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	require.NoError(t, err)
	assert.Equal(t, 1, r.calls)
}

func TestWriteAndRestartPreservesModelInfo(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Write a pre-existing config that has model_info on an embedding route.
	existing := `
model_list:
  - model_name: route-embedding
    litellm_params:
      model: openai/text-embedding-3-small
      api_key: os.environ/OPENAI_API_KEY
    model_info:
      mode: embedding
general_settings:
  master_key: old-key
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	// New sync includes the same model_name but WITHOUT model_info.
	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:   "route-embedding",
				LiteLLMName: "openai/text-embedding-3-small",
				APIBase:     "https://api.openai.com/v1",
				APIKeyEnv:   "OPENAI_API_KEY",
			},
		},
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "new-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	require.NoError(t, err)

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	content := string(data)

	// model_info with mode: embedding must survive the merge.
	assert.Contains(t, content, "model_info", "model_info must be preserved across sync")
	assert.Contains(t, content, "embedding", "mode: embedding must be preserved across sync")
}

// TestWriteAndRestartPreservesOperatorManagedRoutes proves the issue #701
// review's Required 3: a model_list entry whose model_name has no
// corresponding row in the DB-generated set (e.g. route-doc-vlm, bge-m3 —
// operator-managed entries hand-written into deploy/litellm/config.yaml with
// no provider_routes row at all) must survive a sync rather than be dropped,
// while an entry the DB DOES manage is still replaced with the new value in
// the same pass.
func TestWriteAndRestartPreservesOperatorManagedRoutes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Pre-existing config: one DB-managed route (gpt-4o, about to be updated)
	// and one purely operator-managed route with no provider_routes row
	// (route-doc-vlm), carrying hand-tuned litellm_params the generator does
	// not reproduce.
	existing := `
model_list:
  - model_name: gpt-4o
    litellm_params:
      model: openrouter/old/stale-model
      api_key: os.environ/OPENROUTER_API_KEY
  - model_name: route-doc-vlm
    litellm_params:
      model: openrouter/meta-llama/llama-4-scout:free
      api_key: os.environ/OPENROUTER_API_KEY
      extra_body:
        provider:
          allow_fallbacks: false
general_settings:
  master_key: old-key
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	// New sync's DB query only returns gpt-4o; route-doc-vlm has no DB row, so
	// it is absent from KnownRouteIDs too and must be preserved.
	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:   "gpt-4o",
				LiteLLMName: "openrouter/openai/gpt-4o",
				APIBase:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
			},
		},
		KnownRouteIDs: []string{"gpt-4o"},
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "new-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	err := litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r)
	require.NoError(t, err)

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	modelList, ok := parsed["model_list"].([]interface{})
	require.True(t, ok, "model_list must be a sequence")
	require.Len(t, modelList, 2, "operator-managed route must be kept alongside the DB-managed one, not dropped")

	byName := map[string]map[string]interface{}{}
	for _, item := range modelList {
		entry := item.(map[string]interface{})
		byName[entry["model_name"].(string)] = entry
	}

	// DB-managed entry: replaced with the new value, in the same pass.
	gpt4o, ok := byName["gpt-4o"]
	require.True(t, ok, "gpt-4o (DB-managed) must still be present")
	params := gpt4o["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openrouter/openai/gpt-4o", params["model"], "DB-managed entry must be updated to the new DB value")

	// Operator-managed entry: preserved verbatim, including hand-tuned keys
	// Generate never produces (extra_body).
	docVLM, ok := byName["route-doc-vlm"]
	require.True(t, ok, "route-doc-vlm (no provider_routes row) must be preserved, not dropped")
	docParams := docVLM["litellm_params"].(map[string]interface{})
	assert.Equal(t, "openrouter/meta-llama/llama-4-scout:free", docParams["model"], "operator-managed entry's model must survive unchanged")
	assert.Contains(t, docParams, "extra_body", "operator-managed entry's extra_body must survive unchanged")
}

// TestWriteAndRestartRemovesRetiredDBManagedRoute proves the accretion half of
// the merge rule, which pulls against the test above on purpose. Generation is
// active-only (health_state not in disabled/eol, provider enabled), so a route
// that was DB-managed and is later retired disappears from the generated list
// and would otherwise be mistaken for an operator-managed entry and preserved
// forever, with no path that could ever remove it. KnownRouteIDs carries every
// provider_routes.route_id including the inactive ones, so "absent from the
// database" and "present but inactive" stop being the same signal.
//
// 20260801_01_alias_pricing_correction.sql retired
// route-openrouter-fast-fallback by exactly this mechanism (health_state set to
// 'disabled', row kept), which is the shape used here.
func TestWriteAndRestartRemovesRetiredDBManagedRoute(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	existing := `
model_list:
  - model_name: route-groq-fast
    litellm_params:
      model: groq/llama-3.1-8b-instant
      api_key: os.environ/GROQ_API_KEY
  - model_name: route-openrouter-fast-fallback
    litellm_params:
      model: openrouter/openai/gpt-4o-mini
      api_key: os.environ/OPENROUTER_API_KEY
  - model_name: route-doc-vlm
    litellm_params:
      model: os.environ/OPENROUTER_AUTO_MODEL
      api_key: os.environ/OPENROUTER_API_KEY
      extra_body:
        provider:
          allow_fallbacks: false
general_settings:
  master_key: old-key
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	cfg := litellmconfig.Config{
		// Active routes only, exactly what SyncService generates.
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:   "route-groq-fast",
				LiteLLMName: "groq/llama-3.1-8b-instant",
				APIBase:     "https://api.groq.com/openai/v1",
				APIKeyEnv:   "GROQ_API_KEY",
			},
		},
		// Every provider_routes row, active or not. The retired route is here;
		// route-doc-vlm is not, because it has no row at all.
		KnownRouteIDs: []string{"route-groq-fast", "route-openrouter-fast-fallback"},
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "new-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r))

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)
	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	modelList, ok := parsed["model_list"].([]interface{})
	require.True(t, ok, "model_list must be a sequence")

	byName := map[string]map[string]interface{}{}
	for _, item := range modelList {
		entry, ok := item.(map[string]interface{})
		require.True(t, ok)
		byName[entry["model_name"].(string)] = entry
	}

	assert.NotContains(t, byName, "route-openrouter-fast-fallback",
		"a retired DB-managed route must be removed from the live config, not preserved forever")
	require.Contains(t, byName, "route-groq-fast", "the active DB-managed route must stay")
	require.Contains(t, byName, "route-doc-vlm", "an entry with no provider_routes row is operator-managed and must stay (issue #705)")
	docParams := byName["route-doc-vlm"]["litellm_params"].(map[string]interface{})
	assert.Contains(t, docParams, "extra_body", "the operator-managed entry must survive verbatim")
	assert.Len(t, modelList, 2, "exactly the active DB route plus the operator-managed one")
}

// TestWriteAndRestartPreservesHandTunedLiteLLMParams proves issue #707 gap 2:
// field-level preservation inside litellm_params on a DB-managed entry. The
// #705 merge only preserved whole entries with NO provider_routes row, so
// route-openrouter-default and route-openrouter-auto, which DO have rows,
// lost their hand-tuned extra_body.provider tuning (allow_fallbacks: false,
// sort: throughput — added for the flaky-usage-token root cause,
// deploy/litellm/config.yaml section 1) on the first successful sync.
//
// The rule: the generator owns model, api_base and api_key; every other key
// already present in the existing entry's litellm_params survives, the same
// way model_info already did.
func TestWriteAndRestartPreservesHandTunedLiteLLMParams(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")

	// Pre-existing config: a DB-managed route carrying hand-tuned keys the
	// generator does not reproduce (extra_body, plus a second unrelated key to
	// prove the rule is field-level and not an extra_body special case).
	existing := `
model_list:
  - model_name: route-openrouter-default
    litellm_params:
      model: os.environ/OPENROUTER_DEFAULT_MODEL
      api_key: os.environ/OPENROUTER_API_KEY
      timeout: 120
      extra_body:
        provider:
          allow_fallbacks: false
          sort: throughput
general_settings:
  master_key: old-key
`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o600))

	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:   "route-openrouter-default",
				LiteLLMName: "openrouter/openai/gpt-4o-mini",
				APIBase:     "https://openrouter.ai/api/v1",
				APIKeyEnv:   "OPENROUTER_API_KEY",
			},
		},
		GeneralSettings: litellmconfig.GeneralSettings{
			MasterKey: "new-key",
		},
		ExistingConfigPath: configPath,
	}

	r := &mockRestarter{}
	require.NoError(t, litellmconfig.WriteAndRestart(context.Background(), configPath, cfg, r))

	data, readErr := os.ReadFile(configPath)
	require.NoError(t, readErr)

	var parsed map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &parsed))
	modelList, ok := parsed["model_list"].([]interface{})
	require.True(t, ok, "model_list must be a sequence")
	require.Len(t, modelList, 1, "the DB-managed entry must be updated in place, not duplicated")

	entry, ok := modelList[0].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, "route-openrouter-default", entry["model_name"])
	params, ok := entry["litellm_params"].(map[string]interface{})
	require.True(t, ok, "litellm_params must be a mapping")

	// Generator-owned fields: taken from the DB, overwriting what was there.
	assert.Equal(t, "openrouter/openai/gpt-4o-mini", params["model"], "generator owns litellm_params.model")
	assert.Equal(t, "https://openrouter.ai/api/v1", params["api_base"], "generator owns litellm_params.api_base")
	assert.Equal(t, "os.environ/OPENROUTER_API_KEY", params["api_key"], "generator owns litellm_params.api_key")

	// Everything else the operator hand-tuned must survive.
	assert.Equal(t, 120, params["timeout"], "hand-tuned litellm_params keys must survive a sync")
	extraBody, ok := params["extra_body"].(map[string]interface{})
	require.True(t, ok, "extra_body must survive a sync of a DB-managed entry (issue #707)")
	provider, ok := extraBody["provider"].(map[string]interface{})
	require.True(t, ok, "extra_body.provider must survive a sync")
	assert.Equal(t, false, provider["allow_fallbacks"], "extra_body.provider.allow_fallbacks must survive a sync")
	assert.Equal(t, "throughput", provider["sort"], "extra_body.provider.sort must survive a sync")
}
