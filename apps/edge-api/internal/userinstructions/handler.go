package userinstructions

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierr "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
)

// maxBodyBytes bounds the one-field JSON body PUT accepts.
//
// MaxContentLen is counted in runes; this is counted in bytes, and the gap
// between them is entirely JSON escaping. One rune can be four bytes of
// UTF-8, and a character this package strips rather than stores can still
// arrive as a six-byte \uXXXX escape, so a legal 4000-rune body can exceed
// 24 KiB on the wire. 64 KiB clears that with room to spare while staying far
// below the 10 MiB the chat endpoints accept: an instruction body has no
// business being megabytes.
const maxBodyBytes = 64 << 10

// Handler serves the signed-in user's own custom instructions.
//
//	GET /v1/user/instructions   reads them, {"content": ""} when unset
//	PUT /v1/user/instructions   replaces them from {"content": "..."}
//
// There is no DELETE. Clearing the textarea sends an empty string, which Put
// turns into a deleted row, so "cleared" and "never written" are one state
// rather than two that a client would have to tell apart.
//
// The principal is whoever the authentication middleware resolved, and
// nothing in the request influences it: there is no tenant or user parameter
// on either verb, so this surface cannot be pointed at another person's row.
type Handler struct {
	store Store
}

func NewHandler(store Store) *Handler {
	return &Handler{store: store}
}

type instructionsWire struct {
	Content string `json:"content"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil {
		apierr.Write(w, http.StatusUnauthorized, apierr.CodeUnauthenticated, "missing user")
		return
	}
	if user.TenantID == uuid.Nil {
		apierr.Write(w, http.StatusForbidden, apierr.CodeNoTenant, "no tenant for user")
		return
	}
	if user.ID == uuid.Nil {
		// An API-key principal resolves a tenant but no person. Instructions
		// belong to a person, so there is nobody here to have any. Refused
		// rather than answered empty: a key that silently reads and writes
		// the zero-uuid's row would be one shared drawer for every key on the
		// tenant.
		apierr.Write(w, http.StatusForbidden, apierr.CodeForbidden, "custom instructions require a signed-in user")
		return
	}

	switch r.Method {
	case http.MethodGet:
		h.handleGet(w, r, user)
	case http.MethodPut:
		h.handlePut(w, r, user)
	default:
		w.Header().Set("Allow", "GET, PUT")
		apierr.Write(w, http.StatusMethodNotAllowed, apierr.CodeInvalidRequest, "method not allowed")
	}
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, user *auth.User) {
	content, err := h.store.Instructions(r.Context(), user.TenantID, user.ID)
	if err != nil {
		slog.ErrorContext(r.Context(), "user instructions read failed", "err", err)
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "instructions could not be read")
		return
	}
	writeJSON(w, http.StatusOK, instructionsWire{Content: content})
}

func (h *Handler) handlePut(w http.ResponseWriter, r *http.Request, user *auth.User) {
	var req instructionsWire
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(&req); err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "body must be JSON with a content field")
		return
	}

	content, err := Sanitize(req.Content)
	if errors.Is(err, ErrTooLong) {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "custom instructions are limited to 4000 characters")
		return
	}
	if err != nil {
		apierr.Write(w, http.StatusBadRequest, apierr.CodeInvalidRequest, "custom instructions could not be read")
		return
	}

	if err := h.store.Put(r.Context(), user.TenantID, user.ID, content); err != nil {
		slog.ErrorContext(r.Context(), "user instructions write failed", "err", err)
		apierr.Write(w, http.StatusInternalServerError, apierr.CodeInternal, "instructions could not be saved")
		return
	}
	// The sanitized text goes back, not the submitted text: the client renders
	// what was actually stored, so a stripped character is visible immediately
	// rather than on the next page load.
	writeJSON(w, http.StatusOK, instructionsWire{Content: content})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
