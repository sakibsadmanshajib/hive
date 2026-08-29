package featuregate_test

// A gate the console renders is a promise that flipping it changes something.
// Sixteen of the nineteen registered keys keep no such promise: they persist
// to public.tenant_settings and no runtime reader ever looks at them (issues
// #756, #757, #758, tracked together as #762). It was twenty two of twenty
// five until #755 retired the six audit sink keys rather than labelling them.
// Only ENABLE_RAG,
// ENABLE_VOICE and ENABLE_COWORK are mounted anywhere, in
// apps/edge-api/cmd/server/gated_routes.go and main.go.
//
// The registry is where that fact belongs, per #762's first rule: a hardcoded
// list in the frontend would drift the first time a gate gained a reader, and a
// migration can add a key without any code review at all. enforcement_site
// names where the key is read; the wire carries it as a boolean because the
// console needs the yes-or-no, and the prose belongs next to the row an
// engineer is reading in the database.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/featuregate"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/tenant/settings"
)

type enforcementRow struct {
	Key      string `json:"key"`
	Enforced bool   `json:"enforced"`
}

type enforcementResp struct {
	Gates []enforcementRow `json:"gates"`
}

// TestAdmin_List_ReportsEnforcement is the assertion the console depends on: a
// gate with a recorded enforcement site is enforced, one without is not.
func TestAdmin_List_ReportsEnforcement(t *testing.T) {
	store := &fakeAdminStore{
		registry: []settings.GateKey{
			{
				Key:             settings.EnableRAG,
				Label:           "Agent RAG capability",
				Category:        "agents",
				EnforcementSite: "edge-api /v1/rag/ (gated_routes.go)",
			},
			{
				Key:      settings.EnablePublicBilling,
				Label:    "Public billing",
				Category: "billing",
			},
		},
		enabled: map[settings.Key]bool{settings.EnableRAG: true},
	}
	h := featuregate.NewAdminHandler(store).AdminMux()

	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil), adminViewer())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var got enforcementResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Gates) != 2 {
		t.Fatalf("expected 2 gates, got %d", len(got.Gates))
	}

	byKey := map[string]bool{}
	for _, row := range got.Gates {
		byKey[row.Key] = row.Enforced
	}
	if !byKey[string(settings.EnableRAG)] {
		t.Error("a gate with a recorded enforcement site must report enforced=true")
	}
	if byKey[string(settings.EnablePublicBilling)] {
		t.Error("a gate with no recorded enforcement site must report enforced=false")
	}
}

// TestAdmin_List_BlankEnforcementSiteIsNotEnforced pins the direction a
// half-filled row falls. Whitespace is not an enforcement point, and a row
// someone started documenting and left blank must read as unenforced rather
// than accidentally claiming a guarantee.
func TestAdmin_List_BlankEnforcementSiteIsNotEnforced(t *testing.T) {
	store := &fakeAdminStore{
		registry: []settings.GateKey{
			{
				Key:             settings.EnableVoice,
				Label:           "Agent voice capability",
				Category:        "agents",
				EnforcementSite: "   ",
			},
		},
		enabled: map[settings.Key]bool{},
	}
	h := featuregate.NewAdminHandler(store).AdminMux()

	req := withViewer(httptest.NewRequest(http.MethodGet, "/api/v1/admin/feature-gates", nil), adminViewer())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var got enforcementResp
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(got.Gates) != 1 {
		t.Fatalf("expected 1 gate, got %d", len(got.Gates))
	}
	if got.Gates[0].Enforced {
		t.Error("a whitespace-only enforcement site must report enforced=false")
	}
}
