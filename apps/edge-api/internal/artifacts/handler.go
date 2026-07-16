package artifacts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// Store is the minimal interface the handler needs from Repo. Exported so
// main.go can declare a typed nil when the DB pool is absent.
type Store interface {
	CreateArtifact(ctx context.Context, tenantID uuid.UUID, name string) (uuid.UUID, error)
	AddVersion(ctx context.Context, tenantID, artifactID uuid.UUID, storagePath string, sizeBytes int64) (int, error)
	SetPublic(ctx context.Context, tenantID, artifactID uuid.UUID, public bool) error
	GetVersion(ctx context.Context, viewerTenantID, artifactID uuid.UUID, version *int) (VersionRow, error)
}

// BlobStorage is the minimal blob operations the handler needs, satisfied
// by packages/storage.Storage (the same client apps/edge-api/internal/files
// uses against the hive-files bucket).
type BlobStorage interface {
	Upload(ctx context.Context, bucket, key string, body io.Reader, size int64, contentType string) error
	Download(ctx context.Context, bucket, key string) (io.ReadCloser, error)
}

// ClaimsParser is the subset of *auth.SupabaseJWTValidator used to
// optionally resolve a viewer's tenant on the anonymous-reachable
// /artifacts/* serving routes. Those routes sit outside the /v1/ prefix, so
// main.go's authSelectorMiddleware never routes them through JWT
// middleware -- a nil parser here means "always anonymous", matching the
// jwtCfgErr graceful-degradation path already used in main.go when
// Supabase JWT env vars are absent. Only public artifacts serve in that case.
type ClaimsParser interface {
	Parse(ctx context.Context, raw string) (auth.Claims, error)
}

// AuditFunc emits a durable audit event. main.go wires the real
// chat.InsertAuditEvent; tests inject a recorder.
type AuditFunc func(ctx context.Context, action, resourceType, resourceID, severity string,
	tenantID, actorID uuid.UUID, userAgent string, after any)

// Handler serves /v1/artifacts/* (management, tenant-authenticated) and
// /artifacts/* (hosting, optionally anonymous).
type Handler struct {
	store          Store
	blobs          BlobStorage
	bucket         string
	claimsParser   ClaimsParser
	frameAncestors string
	audit          AuditFunc
}

// NewHandler constructs a Handler. claimsParser may be nil (serving routes
// then only ever admit public artifacts). frameAncestors is the CSP
// frame-ancestors directive value for served artifact bytes; an empty
// string defaults to "'none'" (fail-safe: no embedding until a deployment
// explicitly configures the panel origin allowed to iframe artifacts).
func NewHandler(store Store, blobs BlobStorage, bucket string, claimsParser ClaimsParser, frameAncestors string, audit AuditFunc) *Handler {
	if frameAncestors == "" {
		frameAncestors = "'none'"
	}
	if audit == nil {
		audit = func(context.Context, string, string, string, string, uuid.UUID, uuid.UUID, string, any) {}
	}
	return &Handler{
		store:          store,
		blobs:          blobs,
		bucket:         bucket,
		claimsParser:   claimsParser,
		frameAncestors: frameAncestors,
		audit:          audit,
	}
}

// Register mounts every artifacts route on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("/v1/artifacts", h.handleCreate)
	mux.HandleFunc("/v1/artifacts/", h.routeManage)
	mux.HandleFunc("/artifacts/", h.routeServe)
}

// --- Management routes (JWT-authenticated, tenant-scoped) ---

func (h *Handler) handleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
		return
	}
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}

	var req CreateRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxHTMLSize+4096)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	if err := validateHTML(req.HTML); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, err.Error())
		return
	}

	artifactID, err := h.store.CreateArtifact(r.Context(), user.TenantID, req.Name)
	if err != nil {
		log.Printf("artifacts: create: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "artifact creation failed")
		return
	}

	resp, err := h.storeVersion(r.Context(), user.TenantID, artifactID, req.HTML)
	if err != nil {
		log.Printf("artifacts: store version: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "artifact storage failed")
		return
	}

	h.audit(r.Context(), "ARTIFACT_CREATE", "artifact", artifactID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"name": req.Name})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) routeManage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.Write(w, http.StatusMethodNotAllowed, apierrors.CodeInvalidRequest, "method not allowed")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/artifacts/")
	switch {
	case strings.HasSuffix(path, "/versions"):
		h.handleAddVersion(w, r, strings.TrimSuffix(path, "/versions"))
	case strings.HasSuffix(path, "/share"):
		h.handleShare(w, r, strings.TrimSuffix(path, "/share"))
	default:
		apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "unknown route")
	}
}

func (h *Handler) handleAddVersion(w http.ResponseWriter, r *http.Request, idStr string) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	artifactID, err := uuid.Parse(idStr)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid artifact id")
		return
	}

	var req AddVersionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, MaxHTMLSize+4096)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}
	if err := validateHTML(req.HTML); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, err.Error())
		return
	}

	resp, err := h.storeVersion(r.Context(), user.TenantID, artifactID, req.HTML)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "artifact not found")
			return
		}
		log.Printf("artifacts: add version: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "artifact storage failed")
		return
	}

	h.audit(r.Context(), "ARTIFACT_VERSION_ADD", "artifact", artifactID.String(), "INFO",
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"version": resp.Version})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Handler) handleShare(w http.ResponseWriter, r *http.Request, idStr string) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierrors.Write(w, http.StatusUnauthorized, apierrors.CodeUnauthenticated, "unauthenticated")
		return
	}
	artifactID, err := uuid.Parse(idStr)
	if err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid artifact id")
		return
	}

	var req ShareRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		apierrors.Write(w, http.StatusBadRequest, apierrors.CodeInvalidRequest, "invalid request body")
		return
	}

	if err := h.store.SetPublic(r.Context(), user.TenantID, artifactID, req.Public); err != nil {
		if errors.Is(err, ErrNotFound) {
			apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "artifact not found")
			return
		}
		log.Printf("artifacts: set public: %v", err)
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "share update failed")
		return
	}

	// Publicizing an artifact is the one action that turns a private
	// resource world-readable, so it is audited at a higher severity than
	// the reverse (revoking a share) or ordinary create/version writes.
	severity := "INFO"
	if req.Public {
		severity = "WARNING"
	}
	h.audit(r.Context(), "ARTIFACT_SHARE", "artifact", artifactID.String(), severity,
		user.TenantID, user.ID, r.UserAgent(), map[string]any{"public": req.Public})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ShareResponse{ID: artifactID.String(), Public: req.Public})
}

// storeVersion uploads the HTML blob under a version-independent random key
// and then records the next sequential version in the same DB transaction
// (Repo.AddVersion). The blob key does not need to encode the version
// number: only the DB row does, so the upload can happen before the version
// number is known.
func (h *Handler) storeVersion(ctx context.Context, tenantID, artifactID uuid.UUID, html string) (VersionResponse, error) {
	storagePath := fmt.Sprintf("%s/%s/%s.html", tenantID, artifactID, uuid.New())
	size := int64(len(html))
	if err := h.blobs.Upload(ctx, h.bucket, storagePath, strings.NewReader(html), size, "text/html; charset=utf-8"); err != nil {
		return VersionResponse{}, fmt.Errorf("upload blob: %w", err)
	}
	version, err := h.store.AddVersion(ctx, tenantID, artifactID, storagePath, size)
	if err != nil {
		return VersionResponse{}, err
	}
	return VersionResponse{
		ID:           artifactID.String(),
		Version:      version,
		URL:          "/artifacts/" + artifactID.String(),
		VersionedURL: fmt.Sprintf("/artifacts/%s/v/%d", artifactID.String(), version),
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func validateHTML(html string) error {
	if strings.TrimSpace(html) == "" {
		return errors.New("html required")
	}
	if len(html) > MaxHTMLSize {
		return errors.New("html exceeds maximum artifact size")
	}
	return nil
}

// --- Serving routes (anonymous-reachable, RLS-gated at the query) ---

func (h *Handler) routeServe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeServeError(w, http.StatusMethodNotAllowed)
		return
	}

	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/artifacts/"), "/")
	idStr, versionStr, hasVersion := splitServePath(path)
	artifactID, err := uuid.Parse(idStr)
	if err != nil {
		writeServeError(w, http.StatusNotFound)
		return
	}

	var version *int
	if hasVersion {
		n, err := strconv.Atoi(versionStr)
		if err != nil || n < 1 {
			writeServeError(w, http.StatusNotFound)
			return
		}
		version = &n
	}

	viewerTenantID := h.optionalViewerTenant(r)
	row, err := h.store.GetVersion(r.Context(), viewerTenantID, artifactID, version)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			writeServeError(w, http.StatusNotFound)
			return
		}
		log.Printf("artifacts: get version: %v", err)
		writeServeError(w, http.StatusInternalServerError)
		return
	}

	blob, err := h.blobs.Download(r.Context(), h.bucket, row.StoragePath)
	if err != nil {
		log.Printf("artifacts: download blob: %v", err)
		writeServeError(w, http.StatusInternalServerError)
		return
	}
	defer blob.Close()

	h.writeArtifactHeaders(w)
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, blob)
	}
}

// splitServePath parses "{id}" or "{id}/v/{n}" from the trimmed serve path.
// Anything else reports hasVersion=false with an id that will fail
// uuid.Parse, collapsing every malformed shape onto the same 404 path.
func splitServePath(path string) (id, version string, hasVersion bool) {
	parts := strings.Split(path, "/")
	switch len(parts) {
	case 1:
		return parts[0], "", false
	case 3:
		if parts[1] != "v" {
			return "", "", false
		}
		return parts[0], parts[2], true
	default:
		return "", "", false
	}
}

// optionalViewerTenant resolves the caller's tenant from an optional bearer
// JWT for the private-artifact case. Any missing header, missing parser, or
// parse failure (expired/invalid token) falls back to uuid.Nil (anonymous)
// rather than rejecting the request outright.
//
// ponytail: this route's entire purpose is the public fallback, so a bad
// token still gets a fair shot at a public artifact instead of a hard 401 --
// deliberately different from JWTMiddleware, which fails closed.
func (h *Handler) optionalViewerTenant(r *http.Request) uuid.UUID {
	if h.claimsParser == nil {
		return uuid.Nil
	}
	scheme, raw, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || raw == "" {
		return uuid.Nil
	}
	claims, err := h.claimsParser.Parse(r.Context(), raw)
	if err != nil {
		return uuid.Nil
	}
	return claims.TenantID
}

// writeArtifactHeaders sets the strict CSP + no-sniff posture required for
// safely embedding untrusted artifact HTML: no external network of any kind
// (default-src/connect-src 'none'), inline script/style allowed (artifacts
// are self-contained single-file HTML), and frame-ancestors restricted to
// the configured panel origin (or 'none' if unconfigured). The consuming
// iframe (Wave 3.1/3.2 OWUI panel) must additionally set
// sandbox="allow-scripts" with no allow-same-origin, per issue #312.
func (h *Handler) writeArtifactHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; "+
			"img-src data:; connect-src 'none'; base-uri 'none'; form-action 'none'; "+
			"frame-ancestors "+h.frameAncestors+";")
}

func writeServeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(http.StatusText(status)))
}
