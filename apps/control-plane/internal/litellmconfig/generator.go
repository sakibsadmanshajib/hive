// Package litellmconfig generates LiteLLM proxy configuration YAML from the
// current state of provider_routes and custom_providers tables, and triggers
// a controlled LiteLLM container restart so new providers become live without
// manual intervention.
package litellmconfig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
// Merge strategy (critical — preserves operator-managed keys and entries):
//  1. Parse the existing YAML file (if present) into map[string]interface{}.
//  2. Replace model_list entries whose model_name the DB query returned;
//     keep any existing model_list entry whose model_name the DB query did
//     NOT return (see mergeConfig).
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
		if parseErr := yaml.Unmarshal(existingRaw, &existingMap); parseErr != nil {
			return fmt.Errorf("litellmconfig: parse existing config: %w", parseErr)
		}
		if existingMap != nil {
			merged = mergeConfig(existingMap, newMap)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("litellmconfig: read existing config: %w", readErr)
	}
	// If the file does not exist, merged stays as newMap (first-run).

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
//   - model_list entries whose model_name IS returned by the DB query (the
//     generated list) are replaced with the DB-generated value, keeping only
//     their prior model_info (e.g. mode: embedding set by the seed config).
//   - model_list entries whose model_name has NO corresponding DB row are
//     preserved verbatim. provider_routes is not the only source of routes
//     LiteLLM serves: deploy/litellm/config.yaml also carries operator-managed
//     entries with no provider_routes row at all (e.g. route-doc-vlm,
//     bge-m3), and hand-tuned litellm_params keys on DB-managed entries
//     that Generate does not reproduce (e.g. extra_body.provider tuning).
//     Silently dropping either on the first successful sync would be a
//     destructive regression disguised as a bug fix (issue #701 review).
//   - general_settings is merged: master_key updated, all other keys preserved.
//   - All other top-level keys from existing are preserved unchanged.
func mergeConfig(existing, generated map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(existing))
	for k, v := range existing {
		result[k] = v
	}

	newList, _ := generated["model_list"].([]interface{})
	oldList, _ := existing["model_list"].([]interface{})

	// dbManaged records which model_name values the DB query returned this
	// sync; anything in oldList NOT in this set is operator-managed and must
	// survive the merge rather than be dropped.
	dbManaged := map[string]bool{}
	for _, item := range newList {
		if entry, ok := item.(map[string]interface{}); ok {
			if name, ok := entry["model_name"].(string); ok {
				dbManaged[name] = true
			}
		}
	}

	// Build a lookup of existing model_info keyed by model_name, so a
	// DB-managed entry keeps its hand-set model_info (e.g. mode: embedding).
	existingInfo := map[string]interface{}{}
	for _, item := range oldList {
		if entry, ok := item.(map[string]interface{}); ok {
			if name, ok := entry["model_name"].(string); ok {
				if info, exists := entry["model_info"]; exists {
					existingInfo[name] = info
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

	// Append operator-managed entries (no corresponding DB row) unchanged,
	// after the DB-managed ones, and log each so drift between file-managed
	// and DB-managed routes is visible rather than silent.
	mergedList := append([]interface{}{}, newList...)
	for _, item := range oldList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["model_name"].(string)
		if name == "" || dbManaged[name] {
			continue
		}
		slog.Info("litellmconfig: sync: preserving operator-managed model_list entry with no provider_routes row", "model_name", name)
		mergedList = append(mergedList, item)
	}
	result["model_list"] = mergedList

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
