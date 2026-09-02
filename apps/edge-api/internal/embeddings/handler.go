// Package embeddings is the JWT-session half of POST /v1/embeddings.
//
// It exists because of issue #1696. Open WebUI's Python retrieval path is
// configured with RAG_OPENAI_API_BASE_URL and RAG_OPENAI_API_KEY pointing at
// this gateway (deploy/docker/docker-compose.yml), and RAG_OPENAI_API_KEY is
// OWUI_SHIM_KEY. So every chat web search, every document ingest and every
// retrieval query arrived here authenticated as one shared platform account.
// The spend was real and it was metered, and all of it settled against that
// account: a customer's own usage showed nothing they did, one account
// absorbed the embedding spend of every tenant at once, and the per-tenant
// budget work was defeated because the spend never reached the tenant the cap
// applies to.
//
// The fix has two halves and this is the second one. The first is in
// internal/auth: /v1/embeddings joined requiresPerUserAuth, so a shim-key call
// carrying no per-user token is refused instead of billed to the shim, and one
// that does carry a token is rewritten onto that token. This package is what
// then serves it, because the API-key handler cannot: inference.Orchestrator
// resolves an "hk_..." key out of the Authorization header, and a Supabase JWT
// is not one.
//
// Nothing here is a second money path. The hold, the refusals and the single
// terminal state all come from internal/sessionbilling, the same lifecycle
// session chat, RAG chat and the agent-task gate settle through; the charge is
// inference.CreditsForTokens, the same arithmetic the API-key path uses; and
// the response goes through inference.NormalizeEmbeddings, the same
// normalizer. What this file adds is the wiring between them and the fail-
// closed rules that keep spend off a platform account.
package embeddings

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
	apierrors "github.com/sakibsadmanshajib/hive/apps/edge-api/internal/errors"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/inference"
	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/sessionbilling"
)

// surface labels operator logs. Never customer-visible.
const surface = "session embeddings"

// maxRequestBytes bounds the body this handler buffers. Open WebUI batches
// chunks by RAG_EMBEDDING_BATCH_SIZE and a web search indexes whole fetched
// pages, so a legitimate request here is larger than a chat turn but nowhere
// near the 16 MiB the unwrap middleware already allows through in front of it.
const maxRequestBytes = 8 << 20 // 8 MiB

// maxUpstreamResponseBytes bounds the vector document read back from LiteLLM.
// A batch of 256 chunks at a native 4096 dimensions measures about 11.7 MB
// (see rag.embedResponseCeiling for where that figure comes from), so a
// ceiling below that would fail large batches permanently and look like an
// intermittent outage.
const maxUpstreamResponseBytes = 32 << 20 // 32 MiB

// RouteSelectFunc resolves a catalog alias to the route that will serve it.
// Decoupled from inference.RoutingClient so this package can be tested without
// a control-plane, exactly as internal/rag does it.
type RouteSelectFunc func(ctx context.Context, aliasID string) (inference.SelectRouteResult, error)

// DispatchFunc posts one embeddings request upstream.
// inference.LiteLLMClient.Embeddings satisfies it directly.
type DispatchFunc func(ctx context.Context, litellmModel string, body []byte) (*http.Response, error)

// Deps is everything the handler needs. Accounting and Billing are the money
// path: without both, every request is refused rather than served free.
type Deps struct {
	SelectRoute RouteSelectFunc
	Dispatch    DispatchFunc
	Accounting  *inference.AccountingClient
	Billing     sessionbilling.Resolver
}

// Handler serves POST /v1/embeddings for a JWT-session principal.
type Handler struct {
	deps Deps
}

// NewHandler builds the session embeddings handler.
func NewHandler(deps Deps) *Handler { return &Handler{deps: deps} }

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apierrors.WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "Method not allowed", nil)
		return
	}

	// The principal is the whole point of this handler. Without one there is
	// nobody to attribute the spend to, and the thing this must never do is
	// fall back to serving it under the shim's account, which is the defect
	// (#1696). auth.UserFrom is only ever populated by the JWT middleware.
	user, ok := auth.UserFrom(r.Context())
	if !ok || user == nil || user.TenantID == uuid.Nil {
		slog.Warn(surface+" refused, no session principal on the request",
			"path", r.URL.Path)
		apierrors.WriteError(w, http.StatusUnauthorized, "invalid_request_error",
			"This request is not carrying a signed-in user. Sign in again and retry.", nil)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid request body", nil)
		return
	}
	if len(body) > maxRequestBytes {
		apierrors.WriteError(w, http.StatusRequestEntityTooLarge, "invalid_request_error", "Request body too large", nil)
		return
	}

	var req inference.EmbeddingsRequest
	if err := json.Unmarshal(body, &req); err != nil {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Invalid JSON in request body", nil)
		return
	}
	if req.Model == "" {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing required parameter: 'model'", nil)
		return
	}
	if len(req.Input) == 0 || string(req.Input) == "null" || string(req.Input) == `""` {
		apierrors.WriteError(w, http.StatusBadRequest, "invalid_request_error", "Missing required parameter: 'input'", nil)
		return
	}

	if h.deps.SelectRoute == nil {
		slog.Error(surface + " route selection not wired, refusing request")
		sessionbilling.WriteBillingUnavailable(w)
		return
	}
	route, err := h.deps.SelectRoute(r.Context(), req.Model)
	if err != nil {
		switch {
		case errors.Is(err, inference.ErrRouteNotFound):
			apierrors.Write(w, http.StatusNotFound, apierrors.CodeInvalidRequest, "model not found")
		case errors.Is(err, inference.ErrModelNotEntitled):
			apierrors.Write(w, http.StatusForbidden, apierrors.CodeForbidden, "model not available for this workspace")
		default:
			apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, err.Error())
		}
		return
	}

	// Fail closed on the one pricing mode this endpoint cannot settle (D-034).
	// An upstream_actual alias carries no catalog price, and CreditsForTokens
	// deliberately answers zero for one; unlike a chat turn there is no
	// delivered content to price instead, so a request served here would be
	// charged nothing at all while looking completed. sessionbilling's own
	// gate accepts upstream_actual (it is priceable for chat), so the refusal
	// belongs here, at the endpoint that knows it cannot derive the figure.
	// No embedding alias in the catalog is in this mode today; this is what
	// happens when one is, rather than what happens quietly.
	if route.Pricing.IsUpstreamActual() {
		slog.Error(surface+" refused, an embeddings alias cannot settle at an upstream-reported cost",
			"alias", req.Model, "pricing_mode", "upstream_actual")
		sessionbilling.WriteUnpriceableModel(w)
		return
	}

	requestID := uuid.New()
	settle, refused := sessionbilling.Start(r.Context(), w, sessionbilling.Input{
		Accounting: h.deps.Accounting,
		Billing:    h.deps.Billing,
		TenantID:   user.TenantID,
		Route:      route,
		Alias:      req.Model,
		Endpoint:   inference.EndpointEmbeddings,
		RequestID:  requestID,
		Body:       body,
		HoldFloor:  inference.DefaultHoldEmbeddings,
		Surface:    surface,
	})
	if refused {
		return
	}
	settled := false
	releaseReason := "interrupted"
	defer func() {
		if settle != nil && !settled {
			settle.Release(releaseReason)
		}
	}()

	if h.deps.Dispatch == nil {
		releaseReason = "not_wired"
		slog.Error(surface + " dispatch not wired, refusing request")
		sessionbilling.WriteBillingUnavailable(w)
		return
	}
	resp, err := h.deps.Dispatch(r.Context(), route.LiteLLMModelName, body)
	if err != nil {
		releaseReason = "upstream_error"
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, err.Error())
		return
	}
	defer resp.Body.Close()

	upstreamBody, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamResponseBytes))
	if err != nil {
		releaseReason = "read_error"
		apierrors.Write(w, http.StatusInternalServerError, apierrors.CodeInternal, "failed to read upstream response")
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		releaseReason = "upstream_error"
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, resp.StatusCode, string(upstreamBody))
		return
	}

	normalized, usage, err := inference.NormalizeEmbeddings(upstreamBody, req.Model)
	if err != nil {
		releaseReason = "normalize_error"
		apierrors.WriteProviderBlindUpstreamError(w, req.Model, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, normalized)

	// Everything below runs AFTER the response is on the wire, and that
	// ordering is load bearing rather than incidental: Finalize is a
	// synchronous control-plane call bounded at the settlement timeout and
	// retried once, so settling first would put up to two of those in front of
	// a customer who has received nothing. internal/rag states the same reason
	// at the identical point, and a search embeds a batch at a time, so this is
	// on the hot path several times per turn.
	//
	// A failed write does not cancel the charge: the embedding was produced and
	// Hive has already paid for it, so handing the hold back on a broken write
	// would be a caller-controlled free serve (D-055).
	if settle == nil {
		// The Enterprise posture (D-027): no prepaid relationship, nothing to
		// settle. A recorded verdict rather than silence.
		settled = true
		return
	}

	// An embeddings response has no completion side, so the whole charge is the
	// prompt tokens the upstream reported at the alias's own catalog rate. No
	// usage block means nothing measured: the hold is handed back rather than
	// charged at its own size, which is the same verdict the API-key path
	// reaches through settlementCredits for this endpoint. LiteLLM reports usage
	// on every embeddings response, so this is the honest-failure branch rather
	// than a routine one, and it is logged for that reason.
	if usage == nil || usage.PromptTokens <= 0 {
		releaseReason = "unmeasured_usage"
		slog.Warn(surface+" upstream reported no usable usage block, releasing the hold rather than charging an estimate",
			"request_id", requestID, "alias", req.Model)
		return
	}
	if settle.Finalize(inference.CreditsForTokens(route, usage.PromptTokens, 0, 0, 0),
		true, usage.PromptTokens, 0, 0, 0) {
		settled = true
		return
	}
	// The charge did not land. Leaving settled false hands the reservation to
	// the deferred release, so it still reaches a terminal state exactly once
	// rather than stranding the hold behind a lost charge (#616).
	releaseReason = "finalize_failed"
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
