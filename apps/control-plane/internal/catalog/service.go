package catalog

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetSnapshot(ctx context.Context) (CatalogSnapshot, error) {
	aliases, err := s.repo.ListPublicAliases(ctx)
	if err != nil {
		return CatalogSnapshot{}, err
	}

	return buildCatalogSnapshot(aliases), nil
}

// ListModelsForTenant returns the model aliases the given tenant is permitted
// to use. Visibility rules (enforced by the repository query):
//  1. public / preview aliases are visible by default.
//  2. A tenant_model_visibility row with visible=false blocks any alias.
//  3. restricted aliases require an explicit visible=true row.
//
// When tenantID is uuid.Nil (unauthenticated caller), only public/preview
// aliases with no per-tenant override are returned.
func (s *Service) ListModelsForTenant(ctx context.Context, tenantID uuid.UUID) ([]ModelAlias, error) {
	if tenantID == uuid.Nil {
		// Unauthenticated: return public aliases only (no tenant overrides).
		return s.repo.ListPublicAliases(ctx)
	}
	return s.repo.ListAliasesForTenant(ctx, tenantID)
}

// GetSnapshotForTenant is GetSnapshot narrowed to one tenant's entitlement. It
// backs the tenant-scoped /v1/models list so the models a tenant is shown are
// exactly the models the tenant may invoke.
func (s *Service) GetSnapshotForTenant(ctx context.Context, tenantID uuid.UUID) (CatalogSnapshot, error) {
	aliases, err := s.ListModelsForTenant(ctx, tenantID)
	if err != nil {
		return CatalogSnapshot{}, err
	}

	return buildCatalogSnapshot(aliases), nil
}

// IsAliasVisibleToTenant reports whether tenantID may invoke aliasID. It is the
// inference-time half of the same entitlement decision the catalog listing
// makes, and satisfies routing.TenantEntitlements.
//
// A zero tenantID is refused rather than treated as "no restriction": callers on
// the tenant-scoped path must supply a real tenant, and an untenanted principal
// must not reach this check at all.
func (s *Service) IsAliasVisibleToTenant(ctx context.Context, tenantID uuid.UUID, aliasID string) (bool, error) {
	if tenantID == uuid.Nil {
		return false, errors.New("catalog: tenant id is required for an entitlement check")
	}
	if strings.TrimSpace(aliasID) == "" {
		return false, errors.New("catalog: alias id is required for an entitlement check")
	}
	return s.repo.IsAliasVisibleToTenant(ctx, tenantID, aliasID)
}

func buildCatalogSnapshot(aliases []ModelAlias) CatalogSnapshot {
	snapshot := CatalogSnapshot{
		Models:  make([]PublicModel, 0, len(aliases)),
		Catalog: make([]PublicCatalogModel, 0, len(aliases)),
	}

	for _, alias := range aliases {
		if strings.EqualFold(alias.Visibility, "internal") {
			continue
		}

		ownedBy := strings.TrimSpace(alias.OwnedBy)
		if ownedBy == "" {
			ownedBy = "hive"
		}

		snapshot.Models = append(snapshot.Models, PublicModel{
			ID:      alias.AliasID,
			Object:  "model",
			Created: alias.CreatedAt.UTC().Unix(),
			OwnedBy: ownedBy,
			// Trimmed, and empty when the column is empty, so an alias with no
			// curated copy contributes no key at all rather than an empty
			// string the picker would render as a blank subtitle line.
			Name:        strings.TrimSpace(alias.DisplayName),
			Description: strings.TrimSpace(alias.Summary),
		})
		snapshot.Catalog = append(snapshot.Catalog, PublicCatalogModel{
			ID:               alias.AliasID,
			DisplayName:      alias.DisplayName,
			Summary:          alias.Summary,
			CapabilityBadges: append([]string(nil), alias.CapabilityBadges...),
			Pricing: CatalogPricing{
				InputPriceCredits:          alias.InputPriceCredits,
				OutputPriceCredits:         alias.OutputPriceCredits,
				CacheReadPriceCredits:      alias.CacheReadPriceCredits,
				CacheWritePriceCredits:     alias.CacheWritePriceCredits,
				PricingMode:                alias.PricingMode,
				ReservationEstimateCredits: alias.ReservationEstimateCredits,
			},
			Lifecycle: alias.Lifecycle,
		})
	}

	return snapshot
}
