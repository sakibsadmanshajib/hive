package marketplace_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/marketplace"
)

// fakeRepository is a hand-built marketplace.Repository stub for unit tests
// that never need a live Postgres connection.
type fakeRepository struct {
	entries map[uuid.UUID]marketplace.Entry
	enabled map[uuid.UUID]map[uuid.UUID]marketplace.TenantEntry // tenantID -> entryID -> row

	createErr error
	updateErr error
	deleteErr error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{
		entries: make(map[uuid.UUID]marketplace.Entry),
		enabled: make(map[uuid.UUID]map[uuid.UUID]marketplace.TenantEntry),
	}
}

func (f *fakeRepository) ListEntries(context.Context) ([]marketplace.Entry, error) {
	out := make([]marketplace.Entry, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	return out, nil
}

func (f *fakeRepository) GetEntry(_ context.Context, id uuid.UUID) (marketplace.Entry, error) {
	e, ok := f.entries[id]
	if !ok {
		return marketplace.Entry{}, marketplace.ErrNotFound
	}
	return e, nil
}

func (f *fakeRepository) CreateEntry(_ context.Context, e marketplace.Entry) (marketplace.Entry, error) {
	if f.createErr != nil {
		return marketplace.Entry{}, f.createErr
	}
	e.ID = uuid.New()
	f.entries[e.ID] = e
	return e, nil
}

func (f *fakeRepository) UpdateEntry(_ context.Context, id uuid.UUID, name, description string, config json.RawMessage) (marketplace.Entry, error) {
	if f.updateErr != nil {
		return marketplace.Entry{}, f.updateErr
	}
	e, ok := f.entries[id]
	if !ok {
		return marketplace.Entry{}, marketplace.ErrNotFound
	}
	e.Name, e.Description, e.Config = name, description, config
	f.entries[id] = e
	return e, nil
}

func (f *fakeRepository) DeleteEntry(_ context.Context, id uuid.UUID) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.entries[id]; !ok {
		return marketplace.ErrNotFound
	}
	delete(f.entries, id)
	return nil
}

func (f *fakeRepository) EnabledEntryIDs(_ context.Context, tenantID uuid.UUID) (map[uuid.UUID]marketplace.TenantEntry, error) {
	out := make(map[uuid.UUID]marketplace.TenantEntry)
	for id, te := range f.enabled[tenantID] {
		out[id] = te
	}
	return out, nil
}

func (f *fakeRepository) SetEnabled(_ context.Context, tenantID, entryID uuid.UUID, enabled bool, actorID uuid.UUID) error {
	if _, ok := f.entries[entryID]; !ok {
		return marketplace.ErrNotFound
	}
	if f.enabled[tenantID] == nil {
		f.enabled[tenantID] = make(map[uuid.UUID]marketplace.TenantEntry)
	}
	if !enabled {
		delete(f.enabled[tenantID], entryID)
		return nil
	}
	f.enabled[tenantID][entryID] = marketplace.TenantEntry{EntryID: entryID, EnabledBy: actorID}
	return nil
}

func TestService_CreateEntry(t *testing.T) {
	tests := []struct {
		name    string
		kind    marketplace.Kind
		entName string
		config  json.RawMessage
		wantErr error
	}{
		{name: "valid mcp server", kind: marketplace.KindMCPServer, entName: "github", config: json.RawMessage(`{"command":"npx","args":["-y","@modelcontextprotocol/server-github"]}`)},
		{name: "empty config defaults to object", kind: marketplace.KindSkill, entName: "deck-writer", config: nil},
		{name: "invalid kind", kind: marketplace.Kind("not_a_kind"), entName: "x", wantErr: marketplace.ErrInvalidKind},
		{name: "blank name", kind: marketplace.KindRule, entName: "   ", wantErr: marketplace.ErrInvalidName},
		{name: "config not an object", kind: marketplace.KindMCPServer, entName: "bad", config: json.RawMessage(`["not","an","object"]`), wantErr: marketplace.ErrInvalidConfig},
		{name: "config not valid json", kind: marketplace.KindMCPServer, entName: "bad2", config: json.RawMessage(`{not json`), wantErr: marketplace.ErrInvalidConfig},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := marketplace.NewService(newFakeRepository())
			entry, err := svc.CreateEntry(context.Background(), tt.kind, tt.entName, "desc", tt.config, uuid.New())
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("CreateEntry() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("CreateEntry() unexpected err: %v", err)
			}
			if entry.ID == uuid.Nil {
				t.Error("expected a generated entry ID")
			}
			if len(entry.Config) == 0 {
				t.Error("expected Config to default to a non-empty JSON object")
			}
		})
	}
}

func TestService_CreateEntry_DuplicateFromRepository(t *testing.T) {
	repo := newFakeRepository()
	repo.createErr = marketplace.ErrDuplicate
	svc := marketplace.NewService(repo)

	_, err := svc.CreateEntry(context.Background(), marketplace.KindMCPServer, "github", "", nil, uuid.New())
	if !errors.Is(err, marketplace.ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

func TestService_UpdateEntry_BlankName(t *testing.T) {
	svc := marketplace.NewService(newFakeRepository())
	_, err := svc.UpdateEntry(context.Background(), uuid.New(), "", "desc", nil)
	if !errors.Is(err, marketplace.ErrInvalidName) {
		t.Fatalf("expected ErrInvalidName, got %v", err)
	}
}

func TestService_Browse_MergesCatalogAndEnablement(t *testing.T) {
	repo := newFakeRepository()
	svc := marketplace.NewService(repo)
	ctx := context.Background()

	mcp, err := svc.CreateEntry(ctx, marketplace.KindMCPServer, "github", "", json.RawMessage(`{"command":"npx"}`), uuid.New())
	if err != nil {
		t.Fatalf("seed CreateEntry: %v", err)
	}
	skill, err := svc.CreateEntry(ctx, marketplace.KindSkill, "deck-writer", "", nil, uuid.New())
	if err != nil {
		t.Fatalf("seed CreateEntry: %v", err)
	}

	tenantID := uuid.New()
	actorID := uuid.New()
	if err := svc.SetEnabled(ctx, tenantID, mcp.ID, true, actorID); err != nil {
		t.Fatalf("SetEnabled: %v", err)
	}

	entries, enabled, err := svc.Browse(ctx, tenantID)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 catalog entries, got %d", len(entries))
	}
	if _, ok := enabled[mcp.ID]; !ok {
		t.Error("expected mcp entry to be enabled for tenant")
	}
	if _, ok := enabled[skill.ID]; ok {
		t.Error("expected skill entry to remain disabled for tenant")
	}

	// A different tenant sees the same catalog with nothing enabled.
	otherEntries, otherEnabled, err := svc.Browse(ctx, uuid.New())
	if err != nil {
		t.Fatalf("Browse (other tenant): %v", err)
	}
	if len(otherEntries) != 2 {
		t.Fatalf("expected catalog to be tenant-independent, got %d entries", len(otherEntries))
	}
	if len(otherEnabled) != 0 {
		t.Errorf("expected no entries enabled for an unrelated tenant, got %d", len(otherEnabled))
	}
}

func TestService_SetEnabled_UnknownEntry(t *testing.T) {
	svc := marketplace.NewService(newFakeRepository())
	err := svc.SetEnabled(context.Background(), uuid.New(), uuid.New(), true, uuid.New())
	if !errors.Is(err, marketplace.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown entry, got %v", err)
	}
}

func TestService_SetEnabled_DisableRemovesEnablement(t *testing.T) {
	repo := newFakeRepository()
	svc := marketplace.NewService(repo)
	ctx := context.Background()
	tenantID, actorID := uuid.New(), uuid.New()

	entry, err := svc.CreateEntry(ctx, marketplace.KindMCPServer, "github", "", json.RawMessage(`{"command":"npx"}`), uuid.New())
	if err != nil {
		t.Fatalf("seed CreateEntry: %v", err)
	}
	if err := svc.SetEnabled(ctx, tenantID, entry.ID, true, actorID); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := svc.SetEnabled(ctx, tenantID, entry.ID, false, actorID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	_, enabled, err := svc.Browse(ctx, tenantID)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if _, ok := enabled[entry.ID]; ok {
		t.Error("expected entry to no longer be enabled after disable")
	}
}

func TestService_EnabledMCPServers_FiltersByKindAndTenant(t *testing.T) {
	repo := newFakeRepository()
	svc := marketplace.NewService(repo)
	ctx := context.Background()
	tenantID, actorID := uuid.New(), uuid.New()

	mcp, err := svc.CreateEntry(ctx, marketplace.KindMCPServer, "github", "", json.RawMessage(`{"command":"npx","args":["-y","server-github"]}`), uuid.New())
	if err != nil {
		t.Fatalf("seed mcp CreateEntry: %v", err)
	}
	skill, err := svc.CreateEntry(ctx, marketplace.KindSkill, "deck-writer", "", nil, uuid.New())
	if err != nil {
		t.Fatalf("seed skill CreateEntry: %v", err)
	}
	mcpDisabled, err := svc.CreateEntry(ctx, marketplace.KindMCPServer, "slack", "", json.RawMessage(`{"url":"https://example.invalid/mcp"}`), uuid.New())
	if err != nil {
		t.Fatalf("seed disabled mcp CreateEntry: %v", err)
	}

	if err := svc.SetEnabled(ctx, tenantID, mcp.ID, true, actorID); err != nil {
		t.Fatalf("enable mcp: %v", err)
	}
	if err := svc.SetEnabled(ctx, tenantID, skill.ID, true, actorID); err != nil {
		t.Fatalf("enable skill: %v", err)
	}
	// mcpDisabled is deliberately left disabled for tenantID.
	_ = mcpDisabled

	servers, err := svc.EnabledMCPServers(ctx, tenantID)
	if err != nil {
		t.Fatalf("EnabledMCPServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected exactly 1 enabled mcp_server entry, got %d", len(servers))
	}
	if servers[0].ID != mcp.ID {
		t.Errorf("expected the enabled github entry, got %+v", servers[0])
	}

	// An unrelated tenant that enabled nothing gets an empty (non-nil) slice.
	empty, err := svc.EnabledMCPServers(ctx, uuid.New())
	if err != nil {
		t.Fatalf("EnabledMCPServers (other tenant): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("expected 0 enabled servers for unrelated tenant, got %d", len(empty))
	}
}
