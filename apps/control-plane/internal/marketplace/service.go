package marketplace

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// Service validates and orchestrates marketplace catalog operations on top
// of a Repository. It is the single sanctioned write path for both tables;
// Handler never talks to Repository directly.
type Service struct {
	repo Repository
}

// NewService constructs a Service. repo must not be nil.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// ListEntries returns the full catalog, unfiltered by tenant.
func (s *Service) ListEntries(ctx context.Context) ([]Entry, error) {
	return s.repo.ListEntries(ctx)
}

// CreateEntry validates and curates a new catalog entry.
func (s *Service) CreateEntry(ctx context.Context, kind Kind, name, description string, config json.RawMessage, createdBy uuid.UUID) (Entry, error) {
	if !kind.Valid() {
		return Entry{}, ErrInvalidKind
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, ErrInvalidName
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Entry{}, err
	}
	return s.repo.CreateEntry(ctx, Entry{
		Kind:        kind,
		Name:        name,
		Description: description,
		Config:      normalized,
		CreatedBy:   createdBy,
	})
}

// UpdateEntry validates and edits an existing catalog entry's mutable
// fields. Kind is immutable once curated: changing kind would silently
// reinterpret Config's shape for every tenant that already enabled it.
func (s *Service) UpdateEntry(ctx context.Context, id uuid.UUID, name, description string, config json.RawMessage) (Entry, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Entry{}, ErrInvalidName
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Entry{}, err
	}
	return s.repo.UpdateEntry(ctx, id, name, description, normalized)
}

// DeleteEntry removes a catalog entry. ON DELETE CASCADE on
// marketplace_tenant_entries.entry_id means every tenant's enablement of it
// is removed too.
func (s *Service) DeleteEntry(ctx context.Context, id uuid.UUID) error {
	return s.repo.DeleteEntry(ctx, id)
}

// Browse returns the full catalog plus tenantID's enablement map, the two
// pieces the admin connector browse/enable UX renders together.
func (s *Service) Browse(ctx context.Context, tenantID uuid.UUID) ([]Entry, map[uuid.UUID]TenantEntry, error) {
	entries, err := s.repo.ListEntries(ctx)
	if err != nil {
		return nil, nil, err
	}
	enabled, err := s.repo.EnabledEntryIDs(ctx, tenantID)
	if err != nil {
		return nil, nil, err
	}
	return entries, enabled, nil
}

// SetEnabled enables or disables one catalog entry for tenantID.
func (s *Service) SetEnabled(ctx context.Context, tenantID, entryID uuid.UUID, enabled bool, actorID uuid.UUID) error {
	return s.repo.SetEnabled(ctx, tenantID, entryID, enabled, actorID)
}

// EnabledMCPServers returns every KindMCPServer entry tenantID has enabled —
// the read seam apps/agent-engine/internal/marketplaceclient consumes to
// build the OpenHands-native mcpServers config for a pack session.
func (s *Service) EnabledMCPServers(ctx context.Context, tenantID uuid.UUID) ([]Entry, error) {
	entries, enabled, err := s.Browse(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if e.Kind != KindMCPServer {
			continue
		}
		if _, ok := enabled[e.ID]; !ok {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// normalizeConfig defaults an empty/absent config to "{}" and rejects
// anything that does not decode as a JSON object — a curated entry's config
// is always a keyed bag of fields (command/args/env, url/transport, or a free-
// form skill/prompt body), never a bare array or scalar.
func normalizeConfig(config json.RawMessage) (json.RawMessage, error) {
	if len(strings.TrimSpace(string(config))) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var v map[string]any
	if err := json.Unmarshal(config, &v); err != nil {
		return nil, ErrInvalidConfig
	}
	return config, nil
}
