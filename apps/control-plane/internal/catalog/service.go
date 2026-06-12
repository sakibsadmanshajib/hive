package catalog

import (
	"context"
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
		})
		snapshot.Catalog = append(snapshot.Catalog, PublicCatalogModel{
			ID:               alias.AliasID,
			DisplayName:      alias.DisplayName,
			Summary:          alias.Summary,
			CapabilityBadges: append([]string(nil), alias.CapabilityBadges...),
			Pricing: CatalogPricing{
				InputPriceCredits:      alias.InputPriceCredits,
				OutputPriceCredits:     alias.OutputPriceCredits,
				CacheReadPriceCredits:  alias.CacheReadPriceCredits,
				CacheWritePriceCredits: alias.CacheWritePriceCredits,
			},
			Lifecycle: alias.Lifecycle,
		})
	}

	return snapshot
}
