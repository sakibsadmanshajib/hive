package catalog

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// stubRepository implements Repository for unit tests. Visibility filtering
// is performed in-memory using the same rules as the SQL query in pgxRepository.
type stubRepository struct {
	aliases        []ModelAlias
	visibilityRows []TenantModelVisibility
	err            error
	visibilityErr  error
	// routeCapabilities backs ListRouteToolCapabilities. Left nil, every alias
	// reports hive_capabilities.tools = false, which is the correct answer for
	// a fixture that declares no routes at all.
	routeCapabilities []RouteToolCapability
	// routeCapabilitiesErr fails the capability read alone, so a test can prove
	// the snapshot fails loudly rather than serving tools = false for every
	// alias.
	routeCapabilitiesErr error
}

func (s *stubRepository) ListRouteToolCapabilities(_ context.Context) ([]RouteToolCapability, error) {
	if s.routeCapabilitiesErr != nil {
		return nil, s.routeCapabilitiesErr
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.routeCapabilities, nil
}

func (s *stubRepository) ListPublicAliases(_ context.Context) ([]ModelAlias, error) {
	if s.err != nil {
		return nil, s.err
	}
	// Mirror the real pgx query: only public/preview visibility.
	var out []ModelAlias
	for _, a := range s.aliases {
		if a.Visibility == "public" || a.Visibility == "preview" {
			out = append(out, a)
		}
	}
	return out, nil
}

// rowsForTenant mirrors what tenantVisibilityQuery returns: every alias joined
// to this tenant's override, with no verdict applied yet. aliasFilter narrows to
// one alias, exactly as the SQL parameter does.
func (s *stubRepository) rowsForTenant(tenantID uuid.UUID, aliasFilter string) []aliasWithOverride {
	overrides := make(map[string]bool)
	for _, row := range s.visibilityRows {
		if row.TenantID == tenantID {
			overrides[row.AliasID] = row.Visible
		}
	}
	var rows []aliasWithOverride
	for _, alias := range s.aliases {
		if aliasFilter != "" && alias.AliasID != aliasFilter {
			continue
		}
		row := aliasWithOverride{alias: alias}
		if visible, hasRow := overrides[alias.AliasID]; hasRow {
			row.override = &visible
		}
		rows = append(rows, row)
	}
	return rows
}

func (s *stubRepository) ListAliasesForTenant(_ context.Context, tenantID uuid.UUID) ([]ModelAlias, error) {
	if s.err != nil {
		return nil, s.err
	}
	// Resolve through the production predicate rather than a second copy of the
	// rules, so this stub cannot drift from what the database path decides.
	return filterVisibleForTenant(s.rowsForTenant(tenantID, "")), nil
}

func (s *stubRepository) IsAliasVisibleToTenant(_ context.Context, tenantID uuid.UUID, aliasID string) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	return len(filterVisibleForTenant(s.rowsForTenant(tenantID, aliasID))) > 0, nil
}

func (s *stubRepository) GetSnapshot(ctx context.Context) (CatalogSnapshot, error) {
	svc := NewService(s)
	return svc.GetSnapshot(ctx)
}

func (s *stubRepository) GetVisibilityRows(_ context.Context, tenantID uuid.UUID) ([]TenantModelVisibility, error) {
	if s.visibilityErr != nil {
		return nil, s.visibilityErr
	}
	var out []TenantModelVisibility
	for _, row := range s.visibilityRows {
		if row.TenantID == tenantID {
			out = append(out, row)
		}
	}
	return out, nil
}

func (s *stubRepository) UpsertVisibility(_ context.Context, row TenantModelVisibility) error {
	for i, r := range s.visibilityRows {
		if r.TenantID == row.TenantID && r.AliasID == row.AliasID {
			s.visibilityRows[i] = row
			return nil
		}
	}
	s.visibilityRows = append(s.visibilityRows, row)
	return nil
}

func (s *stubRepository) DeleteVisibility(_ context.Context, tenantID uuid.UUID, aliasID string) error {
	for i, r := range s.visibilityRows {
		if r.TenantID == tenantID && r.AliasID == aliasID {
			s.visibilityRows = append(s.visibilityRows[:i], s.visibilityRows[i+1:]...)
			return nil
		}
	}
	return nil
}

func (s *stubRepository) GetAllVisibleTenantsForAlias(_ context.Context, aliasID string) ([]TenantModelVisibility, error) {
	if s.visibilityErr != nil {
		return nil, s.visibilityErr
	}
	var out []TenantModelVisibility
	for _, row := range s.visibilityRows {
		if row.AliasID == aliasID && row.Visible {
			out = append(out, row)
		}
	}
	return out, nil
}

// ListAllAliases mirrors the real pgx query: every alias, no visibility
// filter. Used by the boot-time OWUI reconcile tests.
func (s *stubRepository) ListAllAliases(_ context.Context) ([]ModelAlias, error) {
	if s.err != nil {
		return nil, s.err
	}
	return append([]ModelAlias(nil), s.aliases...), nil
}

func (s *stubRepository) GetAlias(_ context.Context, aliasID string) (ModelAlias, error) {
	if s.err != nil {
		return ModelAlias{}, s.err
	}
	for _, a := range s.aliases {
		if a.AliasID == aliasID {
			return a, nil
		}
	}
	return ModelAlias{}, fmt.Errorf("catalog: model alias not found")
}

// ---------------------------------------------------------------------------
// Existing snapshot tests (unchanged behaviour).
// ---------------------------------------------------------------------------

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
				InputPriceCredits:      int64Ptr(12),
				OutputPriceCredits:     int64Ptr(36),
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
				InputPriceCredits:      int64Ptr(10),
				OutputPriceCredits:     int64Ptr(30),
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
	if got := firstCatalog.Pricing.InputPriceCredits; got == nil || *got != 12 {
		t.Fatalf("expected input price 12, got %v", got)
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
				InputPriceCredits:  int64Ptr(12),
				OutputPriceCredits: int64Ptr(36),
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
				InputPriceCredits:  int64Ptr(1),
				OutputPriceCredits: int64Ptr(1),
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

// ---------------------------------------------------------------------------
// Task 5: ListModelsForTenant filtering tests (TDD — RED before implementation).
// ---------------------------------------------------------------------------

// TC1: tenant with no visibility rows sees all public aliases.
func TestListModelsForTenant_NoRows_SeesPublicAliases(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
			{AliasID: "pub-b", Visibility: "preview", CreatedAt: time.Now()},
		},
	}
	svc := NewService(repo)
	models, err := svc.ListModelsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
}

// TC2: visible=false row for a public alias excludes it.
func TestListModelsForTenant_VisibleFalse_ExcludesPublicAlias(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
			{AliasID: "pub-b", Visibility: "public", CreatedAt: time.Now()},
		},
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: "pub-a", Visible: false},
		},
	}
	svc := NewService(repo)
	models, err := svc.ListModelsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].AliasID != "pub-b" {
		t.Fatalf("expected pub-b, got %q", models[0].AliasID)
	}
}

// TC3: visible=true row for a restricted alias includes it.
func TestListModelsForTenant_VisibleTrue_IncludesRestrictedAlias(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000003")
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "restricted-x", Visibility: "restricted", CreatedAt: time.Now()},
		},
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: "restricted-x", Visible: true},
		},
	}
	svc := NewService(repo)
	models, err := svc.ListModelsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(models))
	}
	if models[0].AliasID != "restricted-x" {
		t.Fatalf("expected restricted-x, got %q", models[0].AliasID)
	}
}

// TC4: restricted alias with no tenant row is excluded.
func TestListModelsForTenant_NoRow_ExcludesRestrictedAlias(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000004")
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "restricted-x", Visibility: "restricted", CreatedAt: time.Now()},
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
		},
	}
	svc := NewService(repo)
	models, err := svc.ListModelsForTenant(context.Background(), tenantID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model (only public), got %d", len(models))
	}
	if models[0].AliasID != "pub-a" {
		t.Fatalf("expected pub-a, got %q", models[0].AliasID)
	}
}

// TC5: unauthenticated (zero tenantID) returns only public aliases.
func TestListModelsForTenant_ZeroTenant_ReturnsPublicOnly(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
			{AliasID: "restricted-x", Visibility: "restricted", CreatedAt: time.Now()},
		},
	}
	svc := NewService(repo)
	models, err := svc.ListModelsForTenant(context.Background(), uuid.Nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("expected 1 model (public only for zero tenant), got %d", len(models))
	}
	if models[0].AliasID != "pub-a" {
		t.Fatalf("expected pub-a, got %q", models[0].AliasID)
	}
}

func int64Ptr(value int64) *int64 {
	return &value
}
