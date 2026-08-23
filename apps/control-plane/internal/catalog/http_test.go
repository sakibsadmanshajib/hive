package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
)

func TestSnapshotHandlerReturnsModelsAndCatalog(t *testing.T) {
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
		},
	}

	handler := NewHandler(NewService(repo))
	req := httptest.NewRequest(http.MethodGet, "/internal/catalog/snapshot", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var snapshot CatalogSnapshot
	if err := json.Unmarshal(rr.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("invalid response JSON: %v", err)
	}

	if len(snapshot.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(snapshot.Models))
	}
	if len(snapshot.Catalog) != 1 {
		t.Fatalf("expected 1 catalog entry, got %d", len(snapshot.Catalog))
	}
	if snapshot.Models[0].ID != "hive-default" {
		t.Fatalf("expected hive-default in models, got %q", snapshot.Models[0].ID)
	}
	if got := snapshot.Catalog[0].Pricing.OutputPriceCredits; got == nil || *got != 36 {
		t.Fatalf("expected output price 36, got %v", got)
	}
}

func TestSnapshotHandlerReturnsServerErrorOnRepositoryFailure(t *testing.T) {
	repo := &stubRepository{err: errors.New("db unavailable")}
	handler := NewHandler(NewService(repo))
	req := httptest.NewRequest(http.MethodGet, "/internal/catalog/snapshot", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "catalog snapshot unavailable") {
		t.Fatalf("expected catalog error body, got %s", rr.Body.String())
	}
}

// ---------------------------------------------------------------------------
// T1: public catalog reads tenant from auth.Viewer, not tenants.User.
// ---------------------------------------------------------------------------

// TestPublicCatalog_WithViewerTenantID_FiltersRestrictedAlias verifies that when
// an auth.Viewer is present in context (from OptionalRequire) with a non-nil
// TenantID, the restricted alias is only shown when the tenant has an explicit
// visible=true grant. Previously the route was mounted without middleware so
// tenants.UserFrom always returned false and restricted aliases were always hidden.
func TestPublicCatalog_WithViewerTenantID_FiltersRestrictedAlias(t *testing.T) {
	tenantID := uuid.MustParse("cccccccc-0000-0000-0000-000000000001")
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
			{AliasID: "restricted-x", Visibility: "restricted", CreatedAt: time.Now()},
		},
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: "restricted-x", Visible: true},
		},
	}
	handler := NewHandler(NewService(repo))

	// Inject viewer with TenantID into context — simulates OptionalRequire middleware.
	viewer := auth.Viewer{UserID: uuid.New(), TenantID: tenantID}
	ctx := auth.WithViewer(context.Background(), viewer)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/models", nil).WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Models []PublicCatalogModel `json:"models"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models (pub-a + restricted-x with grant), got %d: %v", len(resp.Models), resp.Models)
	}
}

// TestPublicCatalog_NoViewer_HidesRestrictedAlias verifies that without an
// auth.Viewer (unauthenticated), restricted aliases are not returned.
func TestPublicCatalog_NoViewer_HidesRestrictedAlias(t *testing.T) {
	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: "pub-a", Visibility: "public", CreatedAt: time.Now()},
			{AliasID: "restricted-x", Visibility: "restricted", CreatedAt: time.Now()},
		},
	}
	handler := NewHandler(NewService(repo))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/catalog/models", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Models []PublicCatalogModel `json:"models"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(resp.Models) != 1 || resp.Models[0].ID != "pub-a" {
		t.Fatalf("expected only pub-a for unauthenticated caller, got %v", resp.Models)
	}
}

// ---------------------------------------------------------------------------
// T2+T4+T5: syncOWUI semantics.
// ---------------------------------------------------------------------------

// stubOWUI records EnsureGroup and SyncModelAccessControl calls for test assertions.
type stubOWUI struct {
	ensuredGroups []string
	syncedModel   string
	syncedGroups  []string
	ensureErr     error
	syncErr       error
	// groupIDs maps group name to returned ID (simulates OWUI group store).
	groupIDs map[string]string
	// calls records every SyncModelAccessControl invocation in order, unlike
	// syncedModel/syncedGroups above (which only hold the most recent call).
	// Reconcile tests walk multiple aliases in one pass and need the full
	// history to assert which aliases were touched and which were not.
	calls []owuiSyncCall
}

type owuiSyncCall struct {
	modelID  string
	groupIDs []string
}

func (s *stubOWUI) EnsureGroup(_ context.Context, name string) (string, error) {
	s.ensuredGroups = append(s.ensuredGroups, name)
	if s.ensureErr != nil {
		return "", s.ensureErr
	}
	if id, ok := s.groupIDs[name]; ok {
		return id, nil
	}
	return "owui-" + name, nil
}

func (s *stubOWUI) SyncModelAccessControl(_ context.Context, modelID string, groupIDs []string) error {
	s.syncedModel = modelID
	s.syncedGroups = groupIDs
	s.calls = append(s.calls, owuiSyncCall{modelID: modelID, groupIDs: groupIDs})
	return s.syncErr
}

// TestSyncOWUI_RestrictedAlias_LastRevoke_UsesPlaceholder proves T2: revoking
// the last grant for a restricted alias must NOT send access_control:null
// (which makes OWUI public). Instead a placeholder group is used so the model
// stays locked down.
func TestSyncOWUI_RestrictedAlias_LastRevoke_UsesPlaceholder(t *testing.T) {
	tenantID := uuid.MustParse("dddddddd-0000-0000-0000-000000000001")
	aliasID := "restricted-x"

	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: aliasID, Visibility: "restricted", CreatedAt: time.Now()},
		},
		// No visible=true rows — last grant has been revoked.
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: aliasID, Visible: false},
		},
	}
	owuiStub := &stubOWUI{groupIDs: map[string]string{
		"hive-restricted-placeholder": "placeholder-id",
	}}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	// Simulate DELETE visibility → triggers syncOWUI.
	req := httptest.NewRequest(http.MethodDelete, "/internal/catalog/visibility/"+tenantID.String()+"/"+aliasID, nil)
	rr := httptest.NewRecorder()
	vh.handleDeleteVisibility(rr, req, tenantID, aliasID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if owuiStub.syncedModel != aliasID {
		t.Fatalf("expected sync for %q, got %q", aliasID, owuiStub.syncedModel)
	}
	// Must send a non-nil allowlist (placeholder) not nil/empty (which = public).
	if len(owuiStub.syncedGroups) == 0 {
		t.Fatal("syncOWUI sent empty group list for restricted alias with no grants: model would become public in OWUI")
	}
	if owuiStub.syncedGroups[0] != "placeholder-id" {
		t.Fatalf("expected placeholder-id, got %q", owuiStub.syncedGroups[0])
	}
}

// TestSyncOWUI_RestrictedAlias_WithGrants_UsesRealGroupIDs proves T4: group
// IDs sent to SyncModelAccessControl must be real OWUI UUIDs returned by
// EnsureGroup, not raw "tenant_<uuid>" name strings.
func TestSyncOWUI_RestrictedAlias_WithGrants_UsesRealGroupIDs(t *testing.T) {
	tenantID := uuid.MustParse("eeeeeeee-0000-0000-0000-000000000001")
	aliasID := "restricted-y"
	groupName := "tenant_" + tenantID.String()
	owuiGroupID := "owui-real-uuid-abc"

	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: aliasID, Visibility: "restricted", CreatedAt: time.Now()},
		},
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: aliasID, Visible: true},
		},
	}
	owuiStub := &stubOWUI{groupIDs: map[string]string{groupName: owuiGroupID}}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	req := httptest.NewRequest(http.MethodPut, "/internal/catalog/visibility/"+tenantID.String()+"/"+aliasID,
		strings.NewReader(`{"visible":true}`))
	rr := httptest.NewRecorder()
	vh.handleUpsertVisibility(rr, req, tenantID, aliasID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if len(owuiStub.syncedGroups) != 1 || owuiStub.syncedGroups[0] != owuiGroupID {
		t.Fatalf("expected real OWUI group UUID %q, got %v", owuiGroupID, owuiStub.syncedGroups)
	}
}

// TestSyncOWUI_PublicAlias_SkipsSync proves T5: public/preview *chat* aliases
// must not have their OWUI access_control overwritten when a visibility block
// row exists. Hive catalog filtering is the enforcement layer; OWUI sync is
// skipped.
//
// The alias here carries real chat badges (["stable","chat","responses"],
// matching hive-default's seed row) rather than an empty CapabilityBadges
// slice. Before issue #772's fix this test passed even with no badges at all,
// which meant it was not actually distinguishing "public chat alias, skip
// sync" from "public alias with no badges, skip sync" — the same assertion
// happened to hold for the wrong reason. TestSyncOWUI_PublicNonChatAlias_
// UsesPlaceholder below is the case that must NOT skip sync despite also
// being Visibility=="public".
func TestSyncOWUI_PublicAlias_SkipsSync(t *testing.T) {
	tenantID := uuid.MustParse("ffffffff-0000-0000-0000-000000000001")
	aliasID := "pub-a"

	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:          aliasID,
				Visibility:       "public",
				CapabilityBadges: []string{"stable", "chat", "responses"},
				CreatedAt:        time.Now(),
			},
		},
		visibilityRows: []TenantModelVisibility{
			{TenantID: tenantID, AliasID: aliasID, Visible: false},
		},
	}
	owuiStub := &stubOWUI{}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	req := httptest.NewRequest(http.MethodDelete, "/internal/catalog/visibility/"+tenantID.String()+"/"+aliasID, nil)
	rr := httptest.NewRecorder()
	vh.handleDeleteVisibility(rr, req, tenantID, aliasID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// OWUI sync must be skipped for public chat aliases — access_control:null
	// is correct (OWUI has no deny-list primitive; Hive catalog is the
	// enforcement layer).
	if owuiStub.syncedModel != "" {
		t.Fatalf("expected no OWUI sync for public chat alias, but SyncModelAccessControl was called for %q", owuiStub.syncedModel)
	}
}

// TestSyncOWUI_PublicNonChatAlias_UsesPlaceholder proves the issue #772 fix:
// an alias carrying a non-chat-modality badge (embeddings/stt/tts/voice) must
// get the OWUI placeholder lockout group even though Visibility=="public" —
// the gate that used to skip OWUI sync for every non-restricted alias must
// not skip this one. Badges mirror the real seed row for hive-embedding-
// default (20260423_01_embedding_alias.sql: ["stable","embeddings"]).
func TestSyncOWUI_PublicNonChatAlias_UsesPlaceholder(t *testing.T) {
	tenantID := uuid.MustParse("11111111-2222-0000-0000-000000000001")
	aliasID := "hive-embedding-default"

	repo := &stubRepository{
		aliases: []ModelAlias{
			{
				AliasID:          aliasID,
				Visibility:       "public",
				CapabilityBadges: []string{"stable", "embeddings"},
				CreatedAt:        time.Now(),
			},
		},
	}
	owuiStub := &stubOWUI{groupIDs: map[string]string{
		"hive-restricted-placeholder": "placeholder-id",
	}}
	vh := NewVisibilityHandler(NewService(repo), owuiStub)

	// Any visibility mutation call triggers syncOWUI; DELETE on an unrelated
	// tenant is enough to exercise it, matching how the admin PUT/DELETE
	// handlers invoke it in production.
	req := httptest.NewRequest(http.MethodDelete, "/internal/catalog/visibility/"+tenantID.String()+"/"+aliasID, nil)
	rr := httptest.NewRecorder()
	vh.handleDeleteVisibility(rr, req, tenantID, aliasID)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if owuiStub.syncedModel != aliasID {
		t.Fatalf("expected OWUI sync for %q despite Visibility==public, got %q", aliasID, owuiStub.syncedModel)
	}
	if len(owuiStub.syncedGroups) == 0 || owuiStub.syncedGroups[0] != "placeholder-id" {
		t.Fatalf("expected placeholder-id lockout group for non-chat-modality alias, got %v", owuiStub.syncedGroups)
	}
}

// TestIsNonChatModality pins the exclusion predicate directly. hive-auto
// (["auto","fallback","preview"], no "chat" badge — see
// 20260331_01_model_catalog.sql) is the case that rules out an inclusion
// ("has chat badge") predicate: it is a legitimate chat-selectable alias that
// would be wrongly hidden by a chat-badge whitelist.
func TestIsNonChatModality(t *testing.T) {
	tests := []struct {
		name   string
		badges []string
		want   bool
	}{
		{name: "hive-default chat badges", badges: []string{"stable", "chat", "responses"}, want: false},
		{name: "hive-fast chat badges", badges: []string{"fast", "chat", "responses"}, want: false},
		{name: "hive-auto has no chat badge but is chat-selectable", badges: []string{"auto", "fallback", "preview"}, want: false},
		{name: "hive-embedding-default badges", badges: []string{"stable", "embeddings"}, want: true},
		{name: "hive-stt badges", badges: []string{"voice", "stt"}, want: true},
		{name: "hive-tts badges", badges: []string{"voice", "tts"}, want: true},
		{name: "nil badges", badges: nil, want: false},
		{name: "empty badges", badges: []string{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isNonChatModality(tt.badges); got != tt.want {
				t.Fatalf("isNonChatModality(%v) = %v, want %v", tt.badges, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T3: VisibilityMux routes are reachable (not 404).
// ---------------------------------------------------------------------------

// TestVisibilityMux_PUT_Returns200 proves T3: the VisibilityMux is properly
// constructed and routes PUT /internal/catalog/visibility/{tenant}/{alias}.
func TestVisibilityMux_PUT_Returns200(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-1111-0000-0000-000000000001")
	aliasID := "restricted-z"

	repo := &stubRepository{
		aliases: []ModelAlias{
			{AliasID: aliasID, Visibility: "restricted", CreatedAt: time.Now()},
		},
	}
	vh := NewVisibilityHandler(NewService(repo), nil) // nil owui: sync disabled
	mux := vh.VisibilityMux()

	path := "/internal/catalog/visibility/" + tenantID.String() + "/" + aliasID
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(`{"visible":true}`))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from VisibilityMux PUT, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestVisibilityMux_GET_Returns200 proves the GET list route is also reachable.
func TestVisibilityMux_GET_Returns200(t *testing.T) {
	tenantID := uuid.MustParse("aaaaaaaa-2222-0000-0000-000000000001")

	repo := &stubRepository{}
	vh := NewVisibilityHandler(NewService(repo), nil)
	mux := vh.VisibilityMux()

	path := "/internal/catalog/visibility/" + tenantID.String()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 from VisibilityMux GET, got %d: %s", rr.Code, rr.Body.String())
	}
}
