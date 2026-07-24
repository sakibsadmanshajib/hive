package ledger

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

type Handler struct {
	svc         *Service
	accountsSvc *accounts.Service
	roleSvc     *platform.RoleService // optional — used to populate Actor.IsAdmin via IsPlatformAdmin
	policy      authz.Policy
}

func NewHandler(svc *Service, accountsSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountsSvc: accountsSvc, policy: authz.NewPolicy()}
}

// WithRoleService returns a copy of the handler wired with the platform role
// service so the admin overlay is enabled for Actor construction. Without it,
// Actor.IsAdmin is always false and platform admins cannot view the credit
// ledger via this handler unless also account-verified.
func (h *Handler) WithRoleService(roleSvc *platform.RoleService) *Handler {
	cloned := *h
	cloned.roleSvc = roleSvc
	return &cloned
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/credits/balance":
		h.handleGetBalance(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/credits/ledger":
		h.handleListEntries(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/invoices":
		h.handleListInvoices(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/accounts/current/invoices/"):
		h.handleGetInvoice(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleGetBalance(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	balance, err := h.svc.GetBalance(r.Context(), accountID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, balance)
}

func (h *Handler) handleListEntries(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	limit := 20
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		limit = parsed
	}

	filter := ListEntriesFilter{
		AccountID: accountID,
		Limit:     limit,
	}

	// Parse optional cursor (UUID of last seen entry).
	if rawCursor := r.URL.Query().Get("cursor"); rawCursor != "" {
		cursorID, err := uuid.Parse(rawCursor)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cursor must be a valid UUID"})
			return
		}
		filter.Cursor = &cursorID
	}

	// Parse optional entry_type filter.
	if rawType := r.URL.Query().Get("type"); rawType != "" {
		et := EntryType(rawType)
		filter.EntryType = &et
	}

	entries, err := h.svc.ListEntriesWithCursor(r.Context(), filter)
	if err != nil {
		writeLedgerError(w, err)
		return
	}

	var nextCursor *uuid.UUID
	if len(entries) == limit {
		last := entries[len(entries)-1].ID
		nextCursor = &last
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     entries,
		"next_cursor": nextCursor,
	})
}

func (h *Handler) handleListInvoices(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	invoices, err := h.svc.ListInvoices(r.Context(), accountID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}

	if invoices == nil {
		invoices = []InvoiceRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoices": invoices})
}

func (h *Handler) handleGetInvoice(w http.ResponseWriter, r *http.Request) {
	accountID, ok := h.resolveCurrentAccountID(w, r)
	if !ok {
		return
	}

	// Extract invoice ID from path: /api/v1/accounts/current/invoices/{id}
	prefix := "/api/v1/accounts/current/invoices/"
	rawID := strings.TrimPrefix(r.URL.Path, prefix)
	rawID = strings.TrimSuffix(rawID, "/")
	invoiceID, err := uuid.Parse(rawID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid invoice ID"})
		return
	}

	invoice, err := h.svc.GetInvoice(r.Context(), accountID, invoiceID)
	if err != nil {
		writeLedgerError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, invoice)
}

func (h *Handler) resolveCurrentAccountID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return uuid.Nil, false
	}

	viewerContext, err := h.accountsSvc.EnsureViewerContext(r.Context(), viewer, parseAccountHeader(r))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return uuid.Nil, false
	}

	// Phase 18: route authz through policy.Can — replaces bare EmailVerified check.
	// isAdmin resolves the real platform-admin overlay when roleSvc is wired
	// (see WithRoleService); without it, a real platform admin who is not
	// account-verified is silently denied ledger access here.
	isAdmin := false
	if h.roleSvc != nil {
		admin, err := h.roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			slog.ErrorContext(r.Context(), "ledger: platform-admin lookup failed",
				slog.String("user_id", viewer.UserID.String()),
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authorization unavailable"})
			return uuid.Nil, false
		}
		isAdmin = admin
	}
	actor := accounts.ActorFor(viewer, accounts.Membership{
		AccountID: viewerContext.CurrentAccount.ID,
		UserID:    viewer.UserID,
		Role:      viewerContext.CurrentAccount.Role,
		Status:    "active",
	}, isAdmin)
	if !h.policy.Can(actor, authz.PermLedgerView) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing billing",
			"code":  "email_verification_required",
		})
		return uuid.Nil, false
	}

	return viewerContext.CurrentAccount.ID, true
}

func writeLedgerError(w http.ResponseWriter, err error) {
	var validationErr *ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Error()})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "ledger entry not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

func parseAccountHeader(r *http.Request) uuid.UUID {
	val := r.Header.Get("X-Hive-Account-ID")
	if val == "" {
		return uuid.Nil
	}

	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil
	}

	return id
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
