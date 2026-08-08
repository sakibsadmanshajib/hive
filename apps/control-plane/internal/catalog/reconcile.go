package catalog

import (
	"context"
	"fmt"
)

// ReconcileOWUISync walks every model alias and applies the OWUI
// access_control state syncOWUI would have applied had the alias passed
// through the admin visibility mutation path (PUT/DELETE
// /internal/catalog/visibility/{tenant}/{alias}). That path only fires on an
// explicit admin action, so it has never touched an alias a migration seeds
// directly into model_aliases (hive-embedding-default, hive-stt, hive-tts) —
// this is why the OWUI chat dropdown has surfaced them as pickable chat
// models since the day they were added (issue #772).
//
// Call once at boot, after the OWUI client and catalog service are both
// wired. Safe to call repeatedly: syncOWUI computes each alias's target state
// from scratch on every call, so this also makes the fix self-healing across
// an Open WebUI image bump that resets access_control on its own model rows.
//
// Best-effort like syncOWUI: a single alias failing to sync (OWUI
// unreachable, alias lookup error) does not abort the walk or fail startup.
func (h *VisibilityHandler) ReconcileOWUISync(ctx context.Context) error {
	if h.owui == nil {
		return nil
	}

	aliases, err := h.svc.repo.ListAllAliases(ctx)
	if err != nil {
		return fmt.Errorf("catalog: reconcile owui sync: list aliases: %w", err)
	}

	for _, alias := range aliases {
		h.syncOWUI(ctx, alias.AliasID)
	}
	return nil
}
