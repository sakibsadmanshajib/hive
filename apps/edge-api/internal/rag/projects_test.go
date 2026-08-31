package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// --- fake project store ---
//
// The fake mirrors the real repository's division of labour exactly, because
// that division is what the tests are about. GetProject is TENANT scoped only,
// the way row level security on app.current_tenant_id is, and it does NOT check
// the owner. If the fake checked the owner it would pass the cross-member test
// no matter what the handler did, which is the "test that cannot fail" shape
// this repository has been bitten by.

func (f *fakeStore) GetProject(_ context.Context, tenantID, projectID uuid.UUID) (ProjectRow, error) {
	p, ok := f.projects[projectID]
	if !ok || p.TenantID != tenantID {
		return ProjectRow{}, ErrProjectForbidden
	}
	return p, nil
}

func (f *fakeStore) CreateProject(_ context.Context, tenantID, ownerUserID uuid.UUID, name, instructions string) (ProjectRow, error) {
	p := ProjectRow{
		ID:           uuid.New(),
		TenantID:     tenantID,
		OwnerUserID:  ownerUserID,
		Name:         name,
		Instructions: instructions,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	f.projects[p.ID] = p
	return p, nil
}

func (f *fakeStore) ListProjects(_ context.Context, tenantID, ownerUserID uuid.UUID) ([]ProjectRow, error) {
	var out []ProjectRow
	for _, p := range f.projects {
		if p.TenantID == tenantID && p.OwnerUserID == ownerUserID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeStore) UpdateProject(_ context.Context, tenantID, ownerUserID, projectID uuid.UUID, name, instructions *string) (ProjectRow, error) {
	p, ok := f.projects[projectID]
	if !ok || p.TenantID != tenantID || p.OwnerUserID != ownerUserID {
		return ProjectRow{}, ErrProjectForbidden
	}
	if name != nil {
		p.Name = *name
	}
	if instructions != nil {
		p.Instructions = *instructions
	}
	p.UpdatedAt = time.Now()
	f.projects[projectID] = p
	return p, nil
}

func (f *fakeStore) DeleteProject(_ context.Context, tenantID, ownerUserID, projectID uuid.UUID) error {
	p, ok := f.projects[projectID]
	if !ok || p.TenantID != tenantID || p.OwnerUserID != ownerUserID {
		return ErrProjectForbidden
	}
	delete(f.projects, projectID)
	return nil
}

func (f *fakeStore) AttachDocument(_ context.Context, tenantID, ownerUserID, projectID, docID uuid.UUID) error {
	p, ok := f.projects[projectID]
	if !ok || p.TenantID != tenantID || p.OwnerUserID != ownerUserID {
		return ErrProjectForbidden
	}
	d, ok := f.docs[docID]
	if !ok || d.TenantID != tenantID {
		return ErrProjectForbidden
	}
	f.attached = append(f.attached, docID)
	return nil
}

// userCtxWith is userCtx with an explicit user id, which every test below needs:
// the whole point is two DIFFERENT users inside one tenant.
func userCtxWith(tenantID, userID uuid.UUID) context.Context {
	return auth.WithUser(context.Background(), &auth.User{ID: userID, TenantID: tenantID})
}

// seedProject puts one project in the store without going through the handler.
func seedProject(f *fakeStore, tenantID, ownerUserID uuid.UUID) ProjectRow {
	p, _ := f.CreateProject(context.Background(), tenantID, ownerUserID, "quarterly filings", "cite the filing")
	return p
}

// TestSearchRefusesAnotherMembersProject is acceptance criterion 6 of the
// issue #1595 spec, the cross-member half.
//
// Two users share one tenant. The project belongs to the owner. The attacker is
// an ordinary, fully authenticated member of the SAME tenant, so row level
// security on app.current_tenant_id is satisfied for them and cannot help: they
// present exactly the tenant id the policy asks for. The only thing standing
// between them and the owner's passages is the ownership check in Go.
//
// Two assertions, and the second is the one that matters. A 404 alone could be
// produced by a handler that queried first and threw the result away, which
// would still have executed a retrieval over another member's documents and
// still have written that member's chunk ids into the audit trail. searchCalled
// proves the refusal happened BEFORE any filtering, which is what the spec
// requires.
func TestSearchRefusesAnotherMembersProject(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()
	attackerID := uuid.New()

	store := newFakeStore()
	store.chunks = []ChunkRow{{
		ID:           uuid.New(),
		DocumentID:   uuid.New(),
		DocumentName: "owner-only.txt",
		Content:      "the owner's private passage",
		Score:        0.1,
	}}
	project := seedProject(store, tenantID, ownerID)

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "what did the filing say", ProjectID: project.ID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(tenantID, attackerID))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("a tenant member reading another member's project must be refused with 404, got %d: %s", w.Code, w.Body.String())
	}
	if store.searchCalled {
		t.Fatal("the store was searched for an unauthorized project: authorization must run before any filtering, not after")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("owner's private passage")) {
		t.Fatal("the refusal body leaked the other member's passage")
	}
}

// TestSearchRefusesAnotherTenantsProject is the cross-tenant half of the same
// criterion.
//
// The caller is authenticated in their own tenant and supplies a project id
// belonging to a different tenant entirely. Row level security is the primary
// control here and the fake models it faithfully (GetProject returns nothing
// for a project outside the caller's tenant), so this test proves the handler
// TURNS that empty read into a refusal rather than quietly falling back to an
// unscoped search over the caller's own corpus. That fallback is the subtle
// failure: it answers 200 with plausible passages and nothing looks wrong.
func TestSearchRefusesAnotherTenantsProject(t *testing.T) {
	callerTenant := uuid.New()
	otherTenant := uuid.New()
	callerID := uuid.New()

	store := newFakeStore()
	store.chunks = []ChunkRow{{
		ID:           uuid.New(),
		DocumentID:   uuid.New(),
		DocumentName: "caller-own.txt",
		Content:      "the caller's own passage",
		Score:        0.2,
	}}
	foreign := seedProject(store, otherTenant, uuid.New())

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "anything", ProjectID: foreign.ID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(callerTenant, callerID))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("a project id from another tenant must be refused with 404, got %d: %s", w.Code, w.Body.String())
	}
	if store.searchCalled {
		t.Fatal("an unauthorized cross-tenant project id reached the store: it must be refused, never silently widened to an unscoped search")
	}
}

// TestSearchScopesToOwnedProject is the positive control for the two refusals
// above. Without it, a handler that refused every project id would pass both of
// them, which is the other half of the "test that cannot fail" trap.
func TestSearchScopesToOwnedProject(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()

	store := newFakeStore()
	store.chunks = []ChunkRow{{
		ID:           uuid.New(),
		DocumentID:   uuid.New(),
		DocumentName: "q3-filing.pdf",
		Content:      "revenue rose",
		Score:        0.05,
	}}
	project := seedProject(store, tenantID, ownerID)

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "revenue", ProjectID: project.ID.String()})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(tenantID, ownerID))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("the project's own owner must be served, got %d: %s", w.Code, w.Body.String())
	}
	if !store.searchCalled {
		t.Fatal("an authorized project search never reached the store")
	}
	if store.lastProjectID != project.ID {
		t.Fatalf("the project scope did not reach the store: want %s, got %s", project.ID, store.lastProjectID)
	}
}

// TestSearchWithoutProjectStaysUnscoped guards the compatibility half: an
// existing API caller that has never heard of projects must keep getting the
// tenant's whole corpus, and must not start being refused.
func TestSearchWithoutProjectStaysUnscoped(t *testing.T) {
	tenantID := uuid.New()
	store := newFakeStore()
	store.chunks = []ChunkRow{{ID: uuid.New(), DocumentID: uuid.New(), DocumentName: "d.txt", Content: "c", Score: 0.3}}

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "anything"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(tenantID, uuid.New()))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("a search with no project must still be served, got %d: %s", w.Code, w.Body.String())
	}
	if store.lastProjectID != uuid.Nil {
		t.Fatalf("an unscoped search must pass uuid.Nil to the store, got %s", store.lastProjectID)
	}
}

// TestSearchRejectsMalformedProjectID: a project_id that is not a UUID is a
// client error, never silently treated as "no project" (which would widen the
// search the caller asked to narrow).
func TestSearchRejectsMalformedProjectID(t *testing.T) {
	store := newFakeStore()
	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "q", ProjectID: "not-a-uuid"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(uuid.New(), uuid.New()))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("a malformed project_id must be 400, got %d: %s", w.Code, w.Body.String())
	}
	if store.searchCalled {
		t.Fatal("a malformed project_id must not reach the store")
	}
}

// TestSearchResultCarriesDocumentName is the citation half of the acceptance
// criterion: the frontend renders a chip naming the file, so the passage has to
// arrive with the file's name on it.
func TestSearchResultCarriesDocumentName(t *testing.T) {
	tenantID := uuid.New()
	store := newFakeStore()
	store.chunks = []ChunkRow{{
		ID:           uuid.New(),
		DocumentID:   uuid.New(),
		DocumentName: "q3-filing.pdf",
		Content:      "revenue rose",
		Score:        0.05,
	}}

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	body, _ := json.Marshal(SearchRequest{Query: "revenue"})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/search", bytes.NewReader(body))
	req = req.WithContext(userCtxWith(tenantID, uuid.New()))
	w := httptest.NewRecorder()
	h.handleSearch(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp SearchResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("expected one result, got %d", len(resp.Results))
	}
	if resp.Results[0].DocumentName != "q3-filing.pdf" {
		t.Fatalf("document_name missing from the search result: got %q", resp.Results[0].DocumentName)
	}
}

// TestProjectCRUDIsOwnerScoped walks the whole project API as one owner and
// then as a second member of the same tenant, and asserts the second member
// cannot read, rename, delete, or attach to anything the first member owns.
//
// Table driven over the four mutating and reading routes rather than four
// near-identical functions, because the interesting variable is the route and
// the expectation is the same for all of them.
func TestProjectCRUDIsOwnerScoped(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()
	otherID := uuid.New()

	store := newFakeStore()
	docID, _ := store.InsertDocument(context.Background(), tenantID, "d.txt", "text/plain", 3)
	project := seedProject(store, tenantID, ownerID)

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	attachBody, _ := json.Marshal(AttachDocumentRequest{DocumentID: docID.String()})
	renameBody := []byte(`{"name":"stolen"}`)

	cases := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{"read", http.MethodGet, "/v1/rag/projects/" + project.ID.String(), nil},
		{"rename", http.MethodPatch, "/v1/rag/projects/" + project.ID.String(), renameBody},
		{"delete", http.MethodDelete, "/v1/rag/projects/" + project.ID.String(), nil},
		{"attach", http.MethodPost, "/v1/rag/projects/" + project.ID.String() + "/documents", attachBody},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reader *bytes.Reader
			if tc.body != nil {
				reader = bytes.NewReader(tc.body)
			} else {
				reader = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(tc.method, tc.path, reader)
			req = req.WithContext(userCtxWith(tenantID, otherID))
			w := httptest.NewRecorder()
			h.routeProjectByID(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("%s of another member's project must be 404, got %d: %s", tc.name, w.Code, w.Body.String())
			}
		})
	}

	// The project survived every attempt above, unrenamed.
	if got, ok := store.projects[project.ID]; !ok || got.Name != "quarterly filings" {
		t.Fatalf("another member's attempts changed the project: %+v", got)
	}
	if len(store.attached) != 0 {
		t.Fatal("another member attached a document to a project they do not own")
	}
}

// TestProjectOwnerCanManageOwnProject is the positive control for the table
// above: the owner really can do all four things, so the 404s there are about
// ownership and not about the routes being broken.
func TestProjectOwnerCanManageOwnProject(t *testing.T) {
	tenantID := uuid.New()
	ownerID := uuid.New()

	store := newFakeStore()
	docID, _ := store.InsertDocument(context.Background(), tenantID, "d.txt", "text/plain", 3)

	var audits []auditRecord
	h := newTestHandler(store, &fakeEmbedder{}, &audits)

	createBody, _ := json.Marshal(ProjectRequest{Name: strptr("filings"), Instructions: strptr("cite the filing")})
	req := httptest.NewRequest(http.MethodPost, "/v1/rag/projects", bytes.NewReader(createBody))
	req = req.WithContext(userCtxWith(tenantID, ownerID))
	w := httptest.NewRecorder()
	h.routeProjects(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var created ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created project: %v", err)
	}
	if created.Instructions != "cite the filing" {
		t.Fatalf("instructions did not round-trip: got %q", created.Instructions)
	}

	// Rename without sending instructions must not blank them, which is the
	// whole reason ProjectRequest's fields are pointers.
	renameBody, _ := json.Marshal(ProjectRequest{Name: strptr("renamed")})
	req = httptest.NewRequest(http.MethodPatch, "/v1/rag/projects/"+created.ID, bytes.NewReader(renameBody))
	req = req.WithContext(userCtxWith(tenantID, ownerID))
	w = httptest.NewRecorder()
	h.routeProjectByID(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var renamed ProjectResponse
	if err := json.NewDecoder(w.Body).Decode(&renamed); err != nil {
		t.Fatalf("decode renamed project: %v", err)
	}
	if renamed.Name != "renamed" {
		t.Fatalf("rename did not apply: got %q", renamed.Name)
	}
	if renamed.Instructions != "cite the filing" {
		t.Fatalf("a rename blanked the instructions: got %q", renamed.Instructions)
	}

	attachBody, _ := json.Marshal(AttachDocumentRequest{DocumentID: docID.String()})
	req = httptest.NewRequest(http.MethodPost, "/v1/rag/projects/"+created.ID+"/documents", bytes.NewReader(attachBody))
	req = req.WithContext(userCtxWith(tenantID, ownerID))
	w = httptest.NewRecorder()
	h.routeProjectByID(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("attach: expected 204, got %d: %s", w.Code, w.Body.String())
	}
	if len(store.attached) != 1 || store.attached[0] != docID {
		t.Fatalf("attach did not reach the store: %v", store.attached)
	}

	req = httptest.NewRequest(http.MethodDelete, "/v1/rag/projects/"+created.ID, bytes.NewReader(nil))
	req = req.WithContext(userCtxWith(tenantID, ownerID))
	w = httptest.NewRecorder()
	h.routeProjectByID(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d: %s", w.Code, w.Body.String())
	}
}

func strptr(s string) *string { return &s }
