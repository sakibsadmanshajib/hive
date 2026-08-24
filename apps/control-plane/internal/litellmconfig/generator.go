// Package litellmconfig generates LiteLLM proxy configuration YAML from the
// current state of provider_routes and custom_providers tables, and triggers
// a controlled LiteLLM container restart so new providers become live without
// manual intervention.
package litellmconfig

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelEntry represents a single LiteLLM model_list entry.
type ModelEntry struct {
	ModelName   string
	LiteLLMName string // e.g. "openrouter/openai/gpt-4o"
	APIBase     string
	APIKeyEnv   string
	// SupportsEmbeddings mirrors provider_capabilities.supports_embeddings for
	// this route and selects the LiteLLM adapter, see litellmModel.
	SupportsEmbeddings bool
}

// GeneralSettings holds the LiteLLM general_settings block.
type GeneralSettings struct {
	MasterKey string
}

// Config is the input to Generate and WriteAndRestart.
type Config struct {
	Models          []ModelEntry
	GeneralSettings GeneralSettings
	// KnownRouteIDs is every provider_routes.route_id, including the routes
	// Models deliberately omits (health_state 'disabled'/'eol', disabled
	// provider). The merge preserves an existing model_list entry only when its
	// model_name is absent from this set, so a retired DB-managed route can
	// finally be removed while a truly operator-managed entry with no
	// provider_routes row at all still survives. Empty means "unknown", which
	// preserves every unmatched entry exactly as before.
	KnownRouteIDs []string
	// KnownGroupNames is every provider_routes.litellm_model_name value,
	// active or not: the names route GROUPS share. A group whose members all
	// go inactive generates no model_list entry at all, so generatedNames
	// never contains it and KnownRouteIDs does not either (it holds route_ids,
	// not the shared gateway name); without this set a fully retired group's
	// stale entry would survive as "operator-managed" forever. The merge drops
	// an unmatched entry whose model_name appears here, exactly as it does for
	// KnownRouteIDs. Empty means "unknown", preserving every unmatched entry.
	KnownGroupNames []string
	// ExistingConfigPath is the path of the on-disk config to merge from.
	// WriteAndRestart reads this file to preserve non-generated keys.
	// If the file does not exist, the config is written from scratch.
	ExistingConfigPath string
}

// Restarter signals the LiteLLM process to reload its config.
type Restarter interface {
	Restart(ctx context.Context) error
}

// litellmModel returns the model string LiteLLM must be given for a route.
//
// For every non-embedding route that string is provider_routes.provider_model
// verbatim, which already carries its native provider prefix (e.g.
// "openrouter/openai/gpt-4o-mini"). Embeddings are the exception: LiteLLM's
// native provider integrations do not map the /embeddings endpoint (the
// openrouter/ one certainly does not), so every embedding entry in
// deploy/litellm/config.yaml reaches the same upstream through the generic
// openai/ adapter plus an explicit api_base instead (see section 5 there).
//
// The adapter is the generator's business, not the database's:
// provider_routes.provider_model stays routing-canonical because the price
// catalog and the "Assert model catalog prices agree with the model LiteLLM
// will call" step in .github/workflows/deploy-demo-box.yml both read that
// column and expect the native prefix. That step canonicalizes a live
// openai/X served from an openrouter.ai api_base back to openrouter/X before
// comparing, so this rewrite keeps it passing untouched (issue #707).
func litellmModel(m ModelEntry) string {
	if !m.SupportsEmbeddings || strings.HasPrefix(m.LiteLLMName, "openai/") {
		return m.LiteLLMName
	}
	if i := strings.Index(m.LiteLLMName, "/"); i >= 0 {
		return "openai/" + m.LiteLLMName[i+1:]
	}
	return "openai/" + m.LiteLLMName
}

// Generate builds a LiteLLM config.yaml byte slice from the provided model
// entries. It does NOT read from DB itself; the caller supplies the entries.
func Generate(cfg Config) ([]byte, error) {
	// model_list is a sequence of maps; yaml.v3 sorts the keys of each map on
	// encode, so entry key order in the output is alphabetical, not source
	// order.
	modelList := make([]map[string]interface{}, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		model := litellmModel(m)
		// api_base is what makes the generic openai/ adapter mean "this
		// provider". Without one, LiteLLM would send the route's request, and
		// the provider's API key, to api.openai.com instead. Keyed on the
		// emitted model string rather than on SupportsEmbeddings, because the
		// openai/ prefix has two sources: the rewrite above, and a
		// provider_model that already carries it (the shape bge-m3 has, which
		// reaches this line with SupportsEmbeddings false). Refuse either way.
		if strings.HasPrefix(model, "openai/") && strings.TrimSpace(m.APIBase) == "" {
			return nil, fmt.Errorf("litellmconfig: route %q resolves to the generic openai/ adapter with no api_base; it would send this provider's key upstream to OpenAI", m.ModelName)
		}
		entry := map[string]interface{}{
			"model_name": m.ModelName,
			"litellm_params": map[string]interface{}{
				"model":    model,
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
//  2. Update model_list entries whose model_name the DB query returned, field
//     by field: the DB owns litellm_params model/api_base/api_key, everything
//     else the entry already carried survives. Keep any existing model_list
//     entry whose model_name is not a provider_routes route_id at all, and drop
//     one that is a route_id but is no longer active (see mergeConfig).
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

	existingRaw, readErr := os.ReadFile(existingPath)
	fileExisted := readErr == nil
	if fileExisted {
		var existingMap map[string]interface{}
		if parseErr := yaml.Unmarshal(existingRaw, &existingMap); parseErr != nil {
			return fmt.Errorf("litellmconfig: parse existing config: %w", parseErr)
		}
		if existingMap != nil {
			merged = mergeConfig(existingMap, newMap, cfg.KnownRouteIDs, cfg.KnownGroupNames)
		}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("litellmconfig: read existing config: %w", readErr)
	}
	// If the file does not exist, merged stays as newMap (first-run).

	finalData, err := yaml.Marshal(merged)
	if err != nil {
		return fmt.Errorf("litellmconfig: marshal merged: %w", err)
	}

	// No-op guard: every caller (including the deploy pipeline, on every
	// deploy regardless of whether anything routing-related changed) hits
	// this unconditionally, and a LiteLLM restart is a real, if brief, chat
	// interruption. yaml.v3 marshals map[string]interface{} keys in a fixed
	// order, and this function is the only writer of configPath, so once one
	// sync has run, its own output is exactly what the next no-op sync's
	// merge would reproduce byte for byte. Skipping here means every future
	// caller gets this for free, rather than each one needing its own diff.
	if fileExisted && bytes.Equal(existingRaw, finalData) {
		slog.Info("litellmconfig: write and restart: config unchanged, skipping restart")
		return nil
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

// mergeParams overlays the generated litellm_params onto the existing ones for
// the same model_name. The generator owns exactly the keys it emits (model,
// api_base, api_key); every other key survives, so hand-tuning a DB-managed
// entry sticks across syncs (e.g. extra_body.provider.allow_fallbacks: false /
// sort: throughput on route-openrouter-default and route-openrouter-auto,
// added for the flaky usage-token root cause; issue #707). Field level, not
// entry level: the DB still owns where the route points, the file still owns
// how it is tuned, and no override column or schema change is needed to carry
// the next hand-tuned key.
func mergeParams(existing, generated interface{}) interface{} {
	gen, genOK := generated.(map[string]interface{})
	if !genOK {
		return existing
	}
	prev, prevOK := existing.(map[string]interface{})
	if !prevOK {
		return gen
	}
	merged := make(map[string]interface{}, len(prev)+len(gen))
	for k, v := range prev {
		merged[k] = v
	}
	for k, v := range gen {
		merged[k] = v
	}
	return merged
}

// mergeConfig merges the newly generated config map into the existing config map.
// Rules:
//   - model_list entries whose model_name IS returned by the DB query (the
//     generated list) keep the DB-generated model_name and litellm_params
//     model/api_base/api_key, and keep every other key they already had:
//     model_info (e.g. mode: embedding set by the seed config) and any
//     hand-tuned litellm_params key the generator does not produce (see
//     mergeParams).
//   - model_list entries whose model_name is NOT a provider_routes route_id at
//     all (absent from knownRouteIDs) are preserved verbatim. provider_routes
//     is not the only source of routes LiteLLM serves: deploy/litellm/config.yaml
//     also carries operator-managed entries with no provider_routes row at all
//     (e.g. route-doc-vlm, bge-m3). Silently dropping either those or the
//     hand-tuned keys above on the first successful sync would be a destructive
//     regression disguised as a bug fix (issue #701 review, issue #707).
//   - model_list entries whose model_name IS a route_id but is missing from the
//     generated list are dropped: the route exists in provider_routes and is no
//     longer active (health_state 'disabled'/'eol', or its provider disabled),
//     e.g. route-openrouter-fast-fallback, retired that way by
//     20260801_01_alias_pricing_correction.sql. Without knownRouteIDs this case
//     is indistinguishable from an operator-managed entry, so a retired route's
//     entry would stay in the live config with no path that could remove it.
//   - general_settings is merged: master_key updated, all other keys preserved.
//   - All other top-level keys from existing are preserved unchanged.
//
// An empty knownRouteIDs means the caller does not know the route set, and every
// unmatched entry is preserved (the pre-knownRouteIDs behaviour). knownGroupNames
// works identically for route GROUPS: names several rows share as their
// litellm_model_name. A group whose members are all retired generates no entry,
// so generatedNames cannot cover it and only this set can retire its stale
// config block.
func mergeConfig(existing, generated map[string]interface{}, knownRouteIDs []string, knownGroupNames []string) map[string]interface{} {
	result := make(map[string]interface{}, len(existing))
	for k, v := range existing {
		result[k] = v
	}

	newList, _ := generated["model_list"].([]interface{})
	oldList, _ := existing["model_list"].([]interface{})

	// generatedNames records which model_name values this sync generated (active
	// routes only); knownRoutes records every provider_routes route_id, active
	// or not. An old entry in neither set is operator-managed and survives; one
	// in knownRoutes but not generatedNames is a retired DB-managed route and is
	// dropped.
	generatedNames := map[string]bool{}
	for _, item := range newList {
		if entry, ok := item.(map[string]interface{}); ok {
			if name, ok := entry["model_name"].(string); ok {
				generatedNames[name] = true
			}
		}
	}
	knownRoutes := make(map[string]bool, len(knownRouteIDs))
	for _, id := range knownRouteIDs {
		knownRoutes[id] = true
	}
	knownGroups := make(map[string]bool, len(knownGroupNames))
	for _, name := range knownGroupNames {
		knownGroups[name] = true
	}

	// Build a lookup of the existing entries keyed by model_name, so a
	// DB-managed entry keeps every part of itself the generator does not own.
	existingByName := map[string]map[string]interface{}{}
	for _, item := range oldList {
		if entry, ok := item.(map[string]interface{}); ok {
			if name, ok := entry["model_name"].(string); ok {
				existingByName[name] = entry
			}
		}
	}
	// Overlay each generated entry onto the entry it replaces, field by field.
	for i, item := range newList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["model_name"].(string)
		prev, exists := existingByName[name]
		if !exists {
			continue
		}
		updated := make(map[string]interface{}, len(prev)+len(entry))
		for k, v := range prev {
			updated[k] = v
		}
		for k, v := range entry {
			updated[k] = v
		}
		updated["litellm_params"] = mergeParams(prev["litellm_params"], entry["litellm_params"])
		newList[i] = updated
	}

	// Append operator-managed entries (no provider_routes row) unchanged, after
	// the DB-managed ones, and log both outcomes so drift between file-managed
	// and DB-managed routes is visible rather than silent.
	mergedList := append([]interface{}{}, newList...)
	for _, item := range oldList {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := entry["model_name"].(string)
		if name == "" || generatedNames[name] {
			continue
		}
		if knownRoutes[name] || knownGroups[name] {
			if !knownRoutes[name] {
				slog.Info("litellmconfig: sync: dropping model_list entry for a route group whose members are all inactive", "model_name", name)
			} else {
				slog.Info("litellmconfig: sync: dropping model_list entry for a provider_routes route that is no longer active", "model_name", name)
			}
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
