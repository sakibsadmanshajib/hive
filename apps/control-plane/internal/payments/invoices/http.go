package invoices

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/hivegpt/hive/apps/control-plane/internal/auth"
)

// =============================================================================
// Phase 14 — Invoice HTTP handler.
//
// Routes (registered in apps/control-plane/internal/platform/http/router.go
// and apps/control-plane/cmd/server/main.go):
//
//	GET /api/v1/invoices?workspace_id=<uuid>      — list workspace invoices
//	GET /api/v1/invoices/{id}                     — fetch one invoice
//	GET /api/v1/invoices/{id}/pdf                 — redirect to short-lived
//	                                                Supabase Storage signed URL
//
// Owner OR member-read (workspace member) — gated via AccessChecker. Cross-
// workspace access surfaces as 404 (not 403) to avoid id-enumeration leakage.
//
// Wire format: BDT subunits only. Zero `amount_usd|usd_|fx_|exchange_rate|
// price_per_credit_usd` keys (regulatory; lint primitive verifies in Task 7).
// =============================================================================

// Handler is the HTTP handler for Phase 14 invoice endpoints.
type Handler struct {
	svc *Service
}

// NewHandler constructs the invoice HTTP handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ServeHTTP routes the three Phase 14 invoice paths.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/invoices":
		h.handleList(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v1/invoices/"):
		rest := strings.TrimPrefix(r.URL.Path, "/api/v1/invoices/")
		if strings.HasSuffix(rest, "/pdf") {
			id := strings.TrimSuffix(rest, "/pdf")
			h.handlePDF(w, r, id)
			return
		}
		h.handleGet(w, r, rest)
	default:
		writeJSON(w, http.StatusNotFound, errorBody("not found"))
	}
}

// =============================================================================
// Wire format
// =============================================================================

type invoiceWire struct {
	ID                 uuid.UUID         `json:"id"`
	WorkspaceID        uuid.UUID         `json:"workspace_id"`
	PeriodStart        string            `json:"period_start"`
	PeriodEnd          string            `json:"period_end"`
	TotalBDTSubunits   int64             `json:"total_bdt_subunits"`
	LineItems          []lineItemWire    `json:"line_items"`
	GeneratedAt        time.Time         `json:"generated_at"`
}

type lineItemWire struct {
	ModelID      string `json:"model_id"`
	RequestCount int64  `json:"request_count"`
	BDTSubunits  int64  `json:"bdt_subunits"`
}

func toInvoiceWire(inv Invoice) invoiceWire {
	items := make([]lineItemWire, 0, len(inv.LineItems))
	for _, it := range inv.LineItems {
		amount := int64(0)
		if it.BDTSubunits != nil {
			amount = it.BDTSubunits.Int64()
		}
		items = append(items, lineItemWire{
			ModelID:      it.ModelID,
			RequestCount: it.RequestCount,
			BDTSubunits:  amount,
		})
	}
	total := int64(0)
	if inv.TotalBDTSubunits != nil {
		total = inv.TotalBDTSubunits.Int64()
	}
	return invoiceWire{
		ID:               inv.ID,
		WorkspaceID:      inv.WorkspaceID,
		PeriodStart:      inv.PeriodStart.Format("2006-01-02"),
		PeriodEnd:        inv.PeriodEnd.Format("2006-01-02"),
		TotalBDTSubunits: total,
		LineItems:        items,
		GeneratedAt:      inv.GeneratedAt,
	}
}

// =============================================================================
// Handlers
// =============================================================================

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized"))
		return
	}
	wsParam := r.URL.Query().Get("workspace_id")
	if wsParam == "" {
		writeJSON(w, http.StatusBadRequest, errorBody("workspace_id required"))
		return
	}
	wsID, err := uuid.Parse(wsParam)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid workspace_id"))
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	items, err := h.svc.ListForWorkspace(r.Context(), viewer.UserID, wsID, limit)
	if err != nil {
		// Membership failure surfaces as ErrInvoiceNotFound -> empty list with
		// 404 (matches single-resource behaviour: no id-enumeration leak).
		if errors.Is(err, ErrInvoiceNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("workspace not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorBody("list failed"))
		return
	}
	out := make([]invoiceWire, 0, len(items))
	for _, inv := range items {
		out = append(out, toInvoiceWire(inv))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (h *Handler) handleGet(w http.ResponseWriter, r *http.Request, idStr string) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized"))
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid invoice id"))
		return
	}
	inv, err := h.svc.Get(r.Context(), viewer.UserID, id)
	if err != nil {
		if errors.Is(err, ErrInvoiceNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("invoice not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorBody("get failed"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invoice": toInvoiceWire(*inv)})
}

func (h *Handler) handlePDF(w http.ResponseWriter, r *http.Request, idStr string) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, errorBody("unauthorized"))
		return
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("invalid invoice id"))
		return
	}
	url, err := h.svc.PDFURL(r.Context(), viewer.UserID, id)
	if err != nil {
		if errors.Is(err, ErrInvoiceNotFound) {
			writeJSON(w, http.StatusNotFound, errorBody("invoice not found"))
			return
		}
		writeJSON(w, http.StatusInternalServerError, errorBody("pdf failed"))
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"invoice-"+id.String()+".pdf\"")
	w.Header().Set("Location", url)
	w.WriteHeader(http.StatusFound)
}

// =============================================================================
// Helpers
// =============================================================================

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func errorBody(msg string) map[string]string {
	return map[string]string{"error": msg}
}
