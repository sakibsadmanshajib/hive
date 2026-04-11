package catalog

import (
	"encoding/json"
	"net/http"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/internal/catalog/snapshot":
		h.handleSnapshot(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/catalog/models":
		h.handlePublicCatalog(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.svc.GetSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog snapshot unavailable"})
		return
	}

	writeJSON(w, http.StatusOK, snapshot)
}

func (h *Handler) handlePublicCatalog(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.svc.GetSnapshot(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "catalog snapshot unavailable"})
		return
	}

	catalog := snapshot.Catalog
	if catalog == nil {
		catalog = []PublicCatalogModel{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": catalog})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
