package artifacts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// --- fakes ---

type fakeArtifact struct {
	tenantID      uuid.UUID
	isPublic      bool
	latestVersion int
	versions      map[int]VersionRow
}

type fakeStore struct {
	mu            sync.Mutex
	artifacts     map[uuid.UUID]*fakeArtifact
	createErr     error
	addVersionErr error
	setPublicErr  error
	getVersionErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{artifacts: map[uuid.UUID]*fakeArtifact{}}
}

func (f *fakeStore) CreateArtifact(ctx context.Context, tenantID uuid.UUID, name string) (uuid.UUID, error) {
	if f.createErr != nil {
		return uuid.Nil, f.createErr
	}
	id := uuid.New()
	f.mu.Lock()
	f.artifacts[id] = &fakeArtifact{tenantID: tenantID, versions: map[int]VersionRow{}}
	f.mu.Unlock()
	return id, nil
}

func (f *fakeStore) AddVersion(ctx context.Context, tenantID, artifactID uuid.UUID, storagePath string, sizeBytes int64) (int, error) {
	if f.addVersionErr != nil {
		return 0, f.addVersionErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.artifacts[artifactID]
	if !ok || a.tenantID != tenantID {
		return 0, ErrNotFound
	}
	a.latestVersion++
	v := a.latestVersion
	a.versions[v] = VersionRow{ArtifactID: artifactID, Version: v, StoragePath: storagePath, SizeBytes: sizeBytes, IsPublic: a.isPublic}
	return v, nil
}

func (f *fakeStore) SetPublic(ctx context.Context, tenantID, artifactID uuid.UUID, public bool) error {
	if f.setPublicErr != nil {
		return f.setPublicErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.artifacts[artifactID]
	if !ok || a.tenantID != tenantID {
		return ErrNotFound
	}
	a.isPublic = public
	for v, row := range a.versions {
		row.IsPublic = public
		a.versions[v] = row
	}
	return nil
}

func (f *fakeStore) GetVersion(ctx context.Context, viewerTenantID, artifactID uuid.UUID, version *int) (VersionRow, error) {
	if f.getVersionErr != nil {
		return VersionRow{}, f.getVersionErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.artifacts[artifactID]
	if !ok {
		return VersionRow{}, ErrNotFound
	}
	// Mimics the RLS OR-combination the real Repo relies on: visible when
	// the viewer's tenant matches, or the artifact is public, regardless
	// of viewer tenant (including the anonymous uuid.Nil case).
	if viewerTenantID != a.tenantID && !a.isPublic {
		return VersionRow{}, ErrNotFound
	}
	v := a.latestVersion
	if version != nil {
		v = *version
	}
	row, ok := a.versions[v]
	if !ok {
		return VersionRow{}, ErrNotFound
	}
	return row, nil
}

type fakeBlobs struct {
	mu          sync.Mutex
	data        map[string][]byte
	uploadErr   error
	downloadErr error
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{data: map[string][]byte{}} }

func (f *fakeBlobs) Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error {
	if f.uploadErr != nil {
		return f.uploadErr
	}
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.data[key] = b
	f.mu.Unlock()
	return nil
}

func (f *fakeBlobs) Download(ctx context.Context, bucket, key string) (io.ReadCloser, error) {
	if f.downloadErr != nil {
		return nil, f.downloadErr
	}
	f.mu.Lock()
	b, ok := f.data[key]
	f.mu.Unlock()
	if !ok {
		return nil, errors.New("blob not found")
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

type fakeClaimsParser struct {
	claims auth.Claims
	err    error
}

func (f *fakeClaimsParser) Parse(ctx context.Context, raw string) (auth.Claims, error) {
	return f.claims, f.err
}

type recordedAudit struct {
	action, resourceID, severity string
	after                        any
}

func newHandlerForTest(store Store, blobs BlobStorage, parser ClaimsParser) (*Handler, *[]recordedAudit) {
	var events []recordedAudit
	audit := func(ctx context.Context, action, resourceType, resourceID, severity string, tenantID, actorID uuid.UUID, userAgent string, after any) {
		events = append(events, recordedAudit{action: action, resourceID: resourceID, severity: severity, after: after})
	}
	h := NewHandler(store, blobs, "hive-files", parser, "", audit)
	return h, &events
}

func withUser(r *http.Request, u *auth.User) *http.Request {
	return r.WithContext(auth.WithUser(r.Context(), u))
}

func newMux(h *Handler) *http.ServeMux {
	mux := http.NewServeMux()
	h.Register(mux)
	return mux
}

// --- create ---

func TestHandleCreate_ReturnsVersionOneAndURLs(t *testing.T) {
	h, audits := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}

	body, _ := json.Marshal(CreateRequest{Name: "demo", HTML: "<html>hi</html>"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp VersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Version != 1 {
		t.Fatalf("Version = %d, want 1", resp.Version)
	}
	if resp.URL != "/artifacts/"+resp.ID {
		t.Fatalf("URL = %q, want /artifacts/%s", resp.URL, resp.ID)
	}
	if resp.VersionedURL != "/artifacts/"+resp.ID+"/v/1" {
		t.Fatalf("VersionedURL = %q, want /artifacts/%s/v/1", resp.VersionedURL, resp.ID)
	}
	if len(*audits) != 1 || (*audits)[0].action != "ARTIFACT_CREATE" {
		t.Fatalf("expected one ARTIFACT_CREATE audit event, got %+v", *audits)
	}
}

func TestHandleCreate_Unauthenticated401(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	body, _ := json.Marshal(CreateRequest{HTML: "<html></html>"})
	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCreate_EmptyHTML400(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	body, _ := json.Marshal(CreateRequest{Name: "demo", HTML: "   "})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreate_HTMLTooLarge400(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	oversized := strings.Repeat("a", MaxHTMLSize+1)
	body, _ := json.Marshal(CreateRequest{Name: "demo", HTML: oversized})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandleCreate_MethodNotAllowed(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	req := httptest.NewRequest(http.MethodGet, "/v1/artifacts", nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// --- add version (redeploy) ---

func createArtifact(t *testing.T, h *Handler, user *auth.User, html string) VersionResponse {
	t.Helper()
	body, _ := json.Marshal(CreateRequest{Name: "demo", HTML: html})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create setup failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp VersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	return resp
}

func TestHandleAddVersion_MintsNextVersionSameURL(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}

	created := createArtifact(t, h, user, "<html>v1</html>")

	body, _ := json.Marshal(AddVersionRequest{HTML: "<html>v2</html>"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/versions", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp VersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Version != 2 {
		t.Fatalf("Version = %d, want 2", resp.Version)
	}
	if resp.URL != created.URL {
		t.Fatalf("redeploy URL = %q, want same stable URL %q", resp.URL, created.URL)
	}
}

func TestHandleAddVersion_UnknownArtifact404(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	body, _ := json.Marshal(AddVersionRequest{HTML: "<html></html>"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+uuid.NewString()+"/versions", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleAddVersion_InvalidArtifactID400(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	body, _ := json.Marshal(AddVersionRequest{HTML: "<html></html>"})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/not-a-uuid/versions", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --- share ---

func TestHandleShare_TogglesPublic(t *testing.T) {
	store := newFakeStore()
	h, audits := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")

	body, _ := json.Marshal(ShareRequest{Public: true})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp ShareResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Public {
		t.Fatalf("Public = false, want true")
	}
	last := (*audits)[len(*audits)-1]
	if last.action != "ARTIFACT_SHARE" || last.severity != "WARNING" {
		t.Fatalf("expected WARNING ARTIFACT_SHARE audit on publicize, got %+v", last)
	}
}

func TestHandleShare_UnknownArtifact404(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	body, _ := json.Marshal(ShareRequest{Public: true})
	req := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+uuid.NewString()+"/share", bytes.NewReader(body)), user)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestHandleShare_Unauthenticated401(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	body, _ := json.Marshal(ShareRequest{Public: true})
	req := httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+uuid.NewString()+"/share", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// --- serve ---

func TestRouteServe_PrivateArtifact_NoAuth404(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>secret</html>")

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for private artifact with no auth", rec.Code)
	}
}

func TestRouteServe_PublicArtifact_NoAuthServesLatest(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")

	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	shareRec := httptest.NewRecorder()
	newMux(h).ServeHTTP(shareRec, shareReq)
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share setup failed: %d", shareRec.Code)
	}

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "<html>v1</html>" {
		t.Fatalf("body = %q, want the stored HTML", rec.Body.String())
	}
}

func TestRouteServe_CSPAndNoSniffHeaders(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")

	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), shareReq)

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "connect-src 'none'") {
		t.Fatalf("CSP missing connect-src 'none': %q", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP missing default frame-ancestors 'none': %q", csp)
	}
	if !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("CSP missing default-src 'none': %q", csp)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", rec.Header().Get("Content-Type"))
	}
}

func TestRouteServe_CustomFrameAncestors(t *testing.T) {
	store := newFakeStore()
	audit := func(context.Context, string, string, string, string, uuid.UUID, uuid.UUID, string, any) {}
	h := NewHandler(store, newFakeBlobs(), "hive-files", nil, "https://chat.hive.ai", audit)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")

	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), shareReq)

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-ancestors https://chat.hive.ai") {
		t.Fatalf("CSP missing configured frame-ancestors: %q", csp)
	}
}

func TestRouteServe_VersionedURLServesSpecificVersion(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")

	verBody, _ := json.Marshal(AddVersionRequest{HTML: "<html>v2</html>"})
	verReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/versions", bytes.NewReader(verBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), verReq)

	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), shareReq)

	// v1 must still be reachable at its own versioned URL after redeploy.
	req := httptest.NewRequest(http.MethodGet, created.VersionedURL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "<html>v1</html>" {
		t.Fatalf("v1 fetch: status=%d body=%q", rec.Code, rec.Body.String())
	}

	// latest (no /v/) must now be v2.
	latestReq := httptest.NewRequest(http.MethodGet, created.URL, nil)
	latestRec := httptest.NewRecorder()
	newMux(h).ServeHTTP(latestRec, latestReq)
	if latestRec.Code != http.StatusOK || latestRec.Body.String() != "<html>v2</html>" {
		t.Fatalf("latest fetch: status=%d body=%q", latestRec.Code, latestRec.Body.String())
	}
}

func TestRouteServe_UnknownVersion404(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")
	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), shareReq)

	req := httptest.NewRequest(http.MethodGet, "/artifacts/"+created.ID+"/v/99", nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRouteServe_InvalidArtifactID404(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	req := httptest.NewRequest(http.MethodGet, "/artifacts/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRouteServe_MethodNotAllowed(t *testing.T) {
	h, _ := newHandlerForTest(newFakeStore(), newFakeBlobs(), nil)
	req := httptest.NewRequest(http.MethodPost, "/artifacts/"+uuid.NewString(), nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func TestRouteServe_PrivateArtifact_AuthenticatedSameTenantOK(t *testing.T) {
	store := newFakeStore()
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	parser := &fakeClaimsParser{claims: auth.Claims{Sub: user.ID, TenantID: user.TenantID}}
	h, _ := newHandlerForTest(store, newFakeBlobs(), parser)
	created := createArtifact(t, h, user, "<html>private</html>")

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for same-tenant authenticated viewer, body=%s", rec.Code, rec.Body.String())
	}
}

func TestRouteServe_PrivateArtifact_AuthenticatedOtherTenant404(t *testing.T) {
	store := newFakeStore()
	owner := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	other := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	parser := &fakeClaimsParser{claims: auth.Claims{Sub: other.ID, TenantID: other.TenantID}}
	h, _ := newHandlerForTest(store, newFakeBlobs(), parser)
	created := createArtifact(t, h, owner, "<html>private</html>")

	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	req.Header.Set("Authorization", "Bearer sometoken")
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for cross-tenant private access", rec.Code)
	}
}

func TestRouteServe_InvalidTokenFallsBackToAnonymous(t *testing.T) {
	store := newFakeStore()
	owner := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	parser := &fakeClaimsParser{err: errors.New("expired")}
	h, _ := newHandlerForTest(store, newFakeBlobs(), parser)
	created := createArtifact(t, h, owner, "<html>private</html>")

	// Private artifact, bad token: falls back to anonymous, still 404 (not
	// a hard auth failure) since the artifact is not public.
	req := httptest.NewRequest(http.MethodGet, created.URL, nil)
	req.Header.Set("Authorization", "Bearer garbage")
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (anonymous fallback, still private)", rec.Code)
	}
}

func TestRouteServe_HeadRequestNoBody(t *testing.T) {
	store := newFakeStore()
	h, _ := newHandlerForTest(store, newFakeBlobs(), nil)
	user := &auth.User{ID: uuid.New(), TenantID: uuid.New()}
	created := createArtifact(t, h, user, "<html>v1</html>")
	shareBody, _ := json.Marshal(ShareRequest{Public: true})
	shareReq := withUser(httptest.NewRequest(http.MethodPost, "/v1/artifacts/"+created.ID+"/share", bytes.NewReader(shareBody)), user)
	newMux(h).ServeHTTP(httptest.NewRecorder(), shareReq)

	req := httptest.NewRequest(http.MethodHead, created.URL, nil)
	rec := httptest.NewRecorder()
	newMux(h).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD response body length = %d, want 0", rec.Body.Len())
	}
}
