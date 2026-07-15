package featuregate

// StateHandler exposes the authenticated tenant's live feature-gate map so an
// Open WebUI (OWUI) Function, or any authenticated client, can read gate state
// at session start and gate its own UI accordingly.
//
// Why this exists (issue #293): OWUI is consumed as a pinned upstream image
// (deploy/docker/docker-compose.yml) with no in-repo fork; its only in-repo
// code surface is the native Function deploy/docker/pipelines/hive_jwt_forward.py
// (installed via OWUI's Functions REST API, see
// apps/web-console/e2e/phase-19/owui/owui.setup.ts). Until now that surface had
// zero awareness of Hive's feature gates: gate state lived only behind
// control-plane's internal-token endpoint and edge-api's Require() middleware,
// neither of which an authenticated end user or OWUI Function can read. This
// handler is the read seam: it reuses the existing per-tenant Gate (30 s cache,
// fail-closed) and the existing request auth context, so no new fetch path,
// cache, or credential is introduced.
//
// The response shape is identical to control-plane's
// GET /internal/featuregate/{tenant_id} body (a {"gates": {key: bool}} object),
// keyed by tenant_setting_key, so a consumer decodes one shape everywhere.
//
// Auth: mounted on the shared edge-api mux, so it flows through the same JWT /
// OWUI-unwrap selector as /v1/rag/ and other tenant-scoped routes; the tenant
// is resolved from the request context, never from a client-supplied field.

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/auth"
)

// NewStateHandler returns an http.Handler serving GET requests with the
// authenticated tenant's feature-gate map. Unauthenticated requests receive the
// same provider-blind 403 as the Require middleware. A control-plane fetch error
// fails closed: the response is 200 with an empty gate map, so a consumer hides
// gated capabilities during an outage rather than showing a button that would
// then 403 on use.
func NewStateHandler(g *Gate) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusMethodNotAllowed)
			_, _ = w.Write([]byte(`{"error":{"code":"METHOD_NOT_ALLOWED","message":"method not allowed","type":"INVALID_REQUEST"}}`))
			return
		}

		user, ok := auth.UserFrom(r.Context())
		if !ok || user == nil {
			writeDenied(w)
			return
		}

		flags, err := g.Fetch(r.Context(), user.TenantID)
		if err != nil {
			// Fail-closed, matching Require's posture. Log so a persistent
			// control-plane outage is visible rather than silently masked.
			slog.Warn("featuregate: state fetch failed, returning empty gate map", "err", err)
			flags = FlagsResponse{}
		}

		gates := flags.Gates
		if gates == nil {
			gates = map[string]bool{}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(FlagsResponse{Gates: gates})
	})
}
