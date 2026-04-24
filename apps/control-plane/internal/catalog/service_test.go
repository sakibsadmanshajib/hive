package catalog

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubRepository struct {
	aliases []ModelAlias
	err     error
}

func (s *stubRepository) ListPublicAliases(_ context.Context) ([]ModelAlias, error) {
	if s.err != nil {
		return nil, s.err
	}

	return append([]ModelAlias(nil), s.aliases...), nil
}

func (s *stubRepository) GetSnapshot(ctx context.Context) (CatalogSnapshot, error) {
	svc := NewService(s)
	return svc.GetSnapshot(ctx)
}

func TestGetSnapshotReturnsHiveOwnedModels(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:                "hive-default",
				OwnedBy:                "hive",
				DisplayName:            "Hive Default",
				Summary:                "Balanced default chat model.",
				Visibility:             "public",
				Lifecycle:              "stable",
				CapabilityBadges:       []string{"stable", "chat", "responses"},
				InputPriceCredits:      12,
				OutputPriceCredits:     36,
				CacheReadPriceCredits:  int64Ptr(2),
				CacheWritePriceCredits: int64Ptr(6),
				CreatedAt:              time.Unix(1_716_935_002, 0).UTC(),
			},
			{
				AliasID:                "hive-auto",
				OwnedBy:                "hive",
				DisplayName:            "Hive Auto",
				Summary:                "Preview fallback-oriented alias.",
				Visibility:             "preview",
				Lifecycle:              "preview",
				CapabilityBadges:       []string{"auto", "fallback", "preview"},
				InputPriceCredits:      10,
				OutputPriceCredits:     30,
				CacheReadPriceCredits:  int64Ptr(1),
				CacheWritePriceCredits: int64Ptr(4),
				CreatedAt:              time.Unix(1_716_935_102, 0).UTC(),
			},
		},
	}

	svc := NewService(repo)

	snapshot, err := svc.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}

	if len(snapshot.Models) != 2 {
		t.Fatalf("expected 2 public models, got %d", len(snapshot.Models))
	}
	if len(snapshot.Catalog) != 2 {
		t.Fatalf("expected 2 catalog entries, got %d", len(snapshot.Catalog))
	}

	firstModel := snapshot.Models[0]
	if firstModel.ID != "hive-default" {
		t.Fatalf("expected first model hive-default, got %q", firstModel.ID)
	}
	if firstModel.Object != "model" {
		t.Fatalf("expected model object type, got %q", firstModel.Object)
	}
	if firstModel.OwnedBy != "hive" {
		t.Fatalf("expected owned_by hive, got %q", firstModel.OwnedBy)
	}
	if firstModel.Created != 1_716_935_002 {
		t.Fatalf("expected unix created time, got %d", firstModel.Created)
	}

	firstCatalog := snapshot.Catalog[0]
	if firstCatalog.ID != "hive-default" {
		t.Fatalf("expected first catalog id hive-default, got %q", firstCatalog.ID)
	}
	if firstCatalog.DisplayName != "Hive Default" {
		t.Fatalf("expected display name Hive Default, got %q", firstCatalog.DisplayName)
	}
	if firstCatalog.Lifecycle != "stable" {
		t.Fatalf("expected stable lifecycle, got %q", firstCatalog.Lifecycle)
	}
	if firstCatalog.Pricing.InputPriceCredits != 12 {
		t.Fatalf("expected input price 12, got %d", firstCatalog.Pricing.InputPriceCredits)
	}
	if firstCatalog.Pricing.CacheReadPriceCredits == nil || *firstCatalog.Pricing.CacheReadPriceCredits != 2 {
		t.Fatalf("expected cache read price 2, got %#v", firstCatalog.Pricing.CacheReadPriceCredits)
	}
}

func TestGetSnapshotOmitsInternalAliases(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:            "hive-default",
				OwnedBy:            "hive",
				DisplayName:        "Hive Default",
				Summary:            "Balanced default chat model.",
				Visibility:         "public",
				Lifecycle:          "stable",
				CapabilityBadges:   []string{"stable", "chat", "responses"},
				InputPriceCredits:  12,
				OutputPriceCredits: 36,
				CreatedAt:          time.Unix(1_716_935_002, 0).UTC(),
			},
			{
				AliasID:            "internal-route",
				OwnedBy:            "hive",
				DisplayName:        "Internal Route",
				Summary:            "Internal only.",
				Visibility:         "internal",
				Lifecycle:          "hidden",
				CapabilityBadges:   []string{"internal"},
				InputPriceCredits:  1,
				OutputPriceCredits: 1,
				CreatedAt:          time.Unix(1_716_935_202, 0).UTC(),
			},
		},
	}

	svc := NewService(repo)

	snapshot, err := svc.GetSnapshot(context.Background())
	if err != nil {
		t.Fatalf("GetSnapshot returned error: %v", err)
	}

	if len(snapshot.Models) != 1 {
		t.Fatalf("expected 1 public model, got %d", len(snapshot.Models))
	}
	if len(snapshot.Catalog) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(snapshot.Catalog))
	}
	if snapshot.Models[0].ID != "hive-default" {
		t.Fatalf("expected only hive-default in public models, got %q", snapshot.Models[0].ID)
	}
	if snapshot.Catalog[0].ID != "hive-default" {
		t.Fatalf("expected only hive-default in catalog, got %q", snapshot.Catalog[0].ID)
	}
}

func TestGetSnapshotPropagatesRepositoryErrors(t *testing.T) {
	repo := &stubRepository{err: errors.New("db unavailable")}
	svc := NewService(repo)

	_, err := svc.GetSnapshot(context.Background())
	if err == nil {
		t.Fatal("expected repository error")
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
