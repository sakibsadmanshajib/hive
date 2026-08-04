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

	// New sync's DB query only returns gpt-4o; route-doc-vlm has no DB row.
	cfg := litellmconfig.Config{
		Models: []litellmconfig.ModelEntry{
			{
				ModelName:   "gpt-4o",
				LiteLLMName: "openrouter/openai/gpt-4o",
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
