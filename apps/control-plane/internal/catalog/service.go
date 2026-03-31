package catalog

import (
	"context"
	"strings"
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
