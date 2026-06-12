// Package litellmconfig generates LiteLLM proxy configuration YAML from the
// current state of provider_routes and custom_providers tables, and triggers
// a controlled LiteLLM container restart so new providers become live without
// manual intervention.
package litellmconfig

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModelEntry represents a single LiteLLM model_list entry.
type ModelEntry struct {
	ModelName   string
	LiteLLMName string // e.g. "openrouter/openai/gpt-4o"
	APIBase     string
	APIKeyEnv   string
}

// GeneralSettings holds the LiteLLM general_settings block.
type GeneralSettings struct {
	MasterKey string
}

// Config is the input to Generate and WriteAndRestart.
type Config struct {
	Models          []ModelEntry
	GeneralSettings GeneralSettings
	// ExistingConfigPath is the path of the on-disk config to merge from.
	// WriteAndRestart reads this file to preserve non-generated keys.
	// If the file does not exist, the config is written from scratch.
	ExistingConfigPath string
}

// Restarter signals the LiteLLM process to reload its config.
type Restarter interface {
	Restart(ctx context.Context) error
}

// Generate builds a LiteLLM config.yaml byte slice from the provided model
// entries. It does NOT read from DB itself; the caller supplies the entries.
func Generate(cfg Config) ([]byte, error) {
	// Build model_list as a sequence of maps to preserve key order.
	modelList := make([]map[string]interface{}, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		entry := map[string]interface{}{
			"model_name": m.ModelName,
			"litellm_params": map[string]interface{}{
				"model":    m.LiteLLMName,
				"api_base": m.APIBase,
				"api_key":  "os.environ/" + m.APIKeyEnv,
			},
		}
		modelList = append(modelList, entry)
	}

	out := map[string]interface{}{
		"model_list": modelList,
		"general_settings": map[string]interface{}{
			"master_key": cfg.GeneralSettings.MasterKey,
		},
	}

	data, err := yaml.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("litellmconfig: marshal: %w", err)
	}
	return data, nil
}

// WriteAndRestart writes the generated config to configPath using an atomic
// merge strategy, then calls restarter.Restart.
//
// Merge strategy (critical — preserves operator-managed keys):
//  1. Parse the existing YAML file (if present) into map[string]interface{}.
//  2. Replace only the model_list key with the newly generated entries.
//  3. Merge general_settings: update master_key, preserve all other keys.
//  4. Marshal the merged map to YAML and write atomically via temp file + rename.
//
// If the existing file does not exist, the config is written from scratch.
func WriteAndRestart(ctx context.Context, configPath string, cfg Config, restarter Restarter) error {
	// Generate the new content.
	newData, err := Generate(cfg)
	if err != nil {
		return fmt.Errorf("litellmconfig: generate: %w", err)
	}

	// Parse the new YAML into a map for merging.
	var newMap map[string]interface{}
	if err := yaml.Unmarshal(newData, &newMap); err != nil {
		return fmt.Errorf("litellmconfig: unmarshal generated: %w", err)
	}

	// Attempt to read and parse the existing config for merge.
	merged := newMap
	existingPath := cfg.ExistingConfigPath
	if existingPath == "" {
		existingPath = configPath
	}

	if existingRaw, readErr := os.ReadFile(existingPath); readErr == nil {
		var existingMap map[string]interface{}
		if parseErr := yaml.Unmarshal(existingRaw, &existingMap); parseErr == nil && existingMap != nil {
			merged = mergeConfig(existingMap, newMap)
		}
	}
	// If the file doesn't exist or can't be parsed, merged stays as newMap (first-run).

	finalData, err := yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("litellmconfig: marshal merged: %w", err)
	}

	// Ensure the target directory exists.
	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("litellmconfig: mkdir: %w", err)
	}

	// Atomic write: temp file in same directory, then rename.
	tmp, err := os.CreateTemp(dir, "litellm-config-*.yaml.tmp")
	if err != nil {
		return fmt.Errorf("litellmconfig: create temp: %w", err)
	}
	tmpName := tmp.Name()

	_, writeErr := tmp.Write(finalData)
	closeErr := tmp.Close()
	if writeErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("litellmconfig: write temp: %w", writeErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("litellmconfig: close temp: %w", closeErr)
	}

	if err := os.Rename(tmpName, configPath); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("litellmconfig: rename: %w", err)
	}

	// Signal restart after successful write.
	if err := restarter.Restart(ctx); err != nil {
		return fmt.Errorf("litellmconfig: restart: %w", err)
	}
	return nil
}

// mergeConfig merges the newly generated config map into the existing config map.
// Rules:
//   - model_list is replaced entirely with the new value.
//   - general_settings is merged: master_key updated, all other keys preserved.
//   - All other top-level keys from existing are preserved unchanged.
func mergeConfig(existing, generated map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(existing))
	for k, v := range existing {
		result[k] = v
	}

	// Replace model_list with newly generated entries, but preserve per-model
	// model_info from the existing config (e.g. mode: embedding set by the
	// seed config on embedding routes).
	newList, _ := generated["model_list"].([]interface{})
	if newList != nil {
		// Build a lookup of existing model_info keyed by model_name.
		existingInfo := map[string]interface{}{}
		if oldList, ok := existing["model_list"].([]interface{}); ok {
			for _, item := range oldList {
				if entry, ok := item.(map[string]interface{}); ok {
					if name, ok := entry["model_name"].(string); ok {
						if info, exists := entry["model_info"]; exists {
							existingInfo[name] = info
						}
					}
				}
			}
		}
		// Restore model_info on each new entry where it existed before.
		for i, item := range newList {
			if entry, ok := item.(map[string]interface{}); ok {
				if name, ok := entry["model_name"].(string); ok {
					if info, exists := existingInfo[name]; exists {
						updated := make(map[string]interface{}, len(entry)+1)
						for k, v := range entry {
							updated[k] = v
						}
						updated["model_info"] = info
						newList[i] = updated
					}
				}
			}
		}
	}
	result["model_list"] = newList

	// Merge general_settings: start from existing, overlay new master_key.
	if newGS, ok := generated["general_settings"].(map[string]interface{}); ok {
		existingGS, _ := existing["general_settings"].(map[string]interface{})
		mergedGS := make(map[string]interface{})
		for k, v := range existingGS {
			mergedGS[k] = v
		}
		if mk, ok := newGS["master_key"]; ok {
			mergedGS["master_key"] = mk
		}
		result["general_settings"] = mergedGS
	}

	return result
}
