package catalog

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestReconcileOWUISync_MigrationSeededNonChatAlias_GetsPlaceholder proves the
// second half of the issue #772 fix: a non-chat-modality alias seeded
// straight into model_aliases by a migration (hive-embedding-default,
// hive-stt, hive-tts in production) — with zero tenant_model_visibility rows
// and zero prior admin visibility mutations — still ends up locked out of the
// OWUI chat picker once ReconcileOWUISync runs. Without this, extending
// syncOWUI's gate alone does nothing on an existing box: syncOWUI only fires
// from the PUT/DELETE admin mutation path, which a migration-seeded row never
// goes through.
//
// A genuinely chat-shaped alias (hive-default) is included alongside it to
// prove the reconcile does not touch aliases outside its scope: syncOWUI
// already skips public chat aliases with no restricted grants, and the walk
// must not somehow force a sync for them too.
func TestReconcileOWUISync_MigrationSeededNonChatAlias_GetsPlaceholder(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:          "hive-embedding-default",
				Visibility:       "public",
				CapabilityBadges: []string{"stable", "embeddings"},
				CreatedAt:        time.Now(),
			},
			{
				AliasID:          "hive-stt",
				Visibility:       "public",
				CapabilityBadges: []string{"voice", "stt"},
				CreatedAt:        time.Now(),
			},
			{
				AliasID:          "hive-default",
				Visibility:       "public",
				CapabilityBadges: []string{"stable", "chat", "responses"},
				CreatedAt:        time.Now(),
			},
		},
		// No visibilityRows at all — models this alias set has never been
		// touched by the admin PUT/DELETE visibility endpoints.
	}
	owuiStub := &stubOWUI{groupIDs: map[string]string{
		"hive-restricted-placeholder": "placeholder-id",
	}}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	if err := vh.ReconcileOWUISync(context.Background()); err != nil {
		t.Fatalf("ReconcileOWUISync returned error: %v", err)
	}

	synced := make(map[string][]string, len(owuiStub.calls))
	for _, call := range owuiStub.calls {
		synced[call.modelID] = call.groupIDs
	}

	for _, aliasID := range []string{"hive-embedding-default", "hive-stt"} {
		groups, ok := synced[aliasID]
		if !ok {
			t.Fatalf("expected ReconcileOWUISync to sync %q, it did not", aliasID)
		}
		if len(groups) == 0 || groups[0] != "placeholder-id" {
			t.Fatalf("expected %q locked with placeholder-id, got %v", aliasID, groups)
		}
	}

	if _, ok := synced["hive-default"]; ok {
		t.Fatalf("expected no OWUI sync for chat-shaped public alias hive-default, but it was synced: %v", synced["hive-default"])
	}
}

// TestReconcileOWUISync_NilOWUI_NoOp proves the reconcile is a no-op (and does
// not touch the repository) when OWUI is not configured, mirroring syncOWUI's
// own nil-owui guard.
func TestReconcileOWUISync_NilOWUI_NoOp(t *testing.T) {
	repo := &stubRepository{err: errors.New("repository must not be queried when OWUI is nil")}
	vh := NewVisibilityHandler(NewService(repo), nil)

	if err := vh.ReconcileOWUISync(context.Background()); err != nil {
		t.Fatalf("expected nil error with nil OWUI client, got %v", err)
	}
}

// TestReconcileOWUISync_RepositoryError_ReturnsError proves a repository
// failure surfaces to the caller (main.go logs it) instead of failing silently.
func TestReconcileOWUISync_RepositoryError_ReturnsError(t *testing.T) {
	repo := &stubRepository{err: errors.New("db unavailable")}
	owuiStub := &stubOWUI{}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	if err := vh.ReconcileOWUISync(context.Background()); err == nil {
		t.Fatal("expected error when ListAllAliases fails, got nil")
	}
}
