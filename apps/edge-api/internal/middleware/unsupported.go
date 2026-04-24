package middleware

import (
	"fmt"
	"net/http"
	"strings"

	apierrors "github.com/hivegpt/hive/apps/edge-api/internal/errors"
	"github.com/hivegpt/hive/apps/edge-api/internal/matrix"
)

// UnsupportedEndpointMiddleware returns middleware that rejects requests to
// endpoints not marked as supported_now in the support matrix. Only applies
// to paths starting with /v1/.
func UnsupportedEndpointMiddleware(m *matrix.SupportMatrix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only check /v1/ paths
			if !strings.HasPrefix(r.URL.Path, "/v1/") {
				next.ServeHTTP(w, r)
				return
			}

			status := m.Lookup(r.Method, r.URL.Path)

			switch status {
			case matrix.StatusSupportedNow:
				next.ServeHTTP(w, r)
				return

			case matrix.StatusPlannedForLaunch:
				code := "endpoint_not_available"
				apierrors.WriteError(w, http.StatusNotFound,
					"unsupported_endpoint",
					fmt.Sprintf("The endpoint %s %s is planned but not yet available. Check the Hive support matrix for current status.", r.Method, r.URL.Path),
					&code,
				)

			case matrix.StatusExplicitlyUnsupported:
				code := "endpoint_unsupported"
				apierrors.WriteError(w, http.StatusNotFound,
					"unsupported_endpoint",
					fmt.Sprintf("The endpoint %s %s is not supported. This endpoint is outside the current Hive launch scope.", r.Method, r.URL.Path),
					&code,
				)

			case matrix.StatusOutOfScope:
				code := "endpoint_out_of_scope"
				apierrors.WriteError(w, http.StatusNotFound,
					"unsupported_endpoint",
					fmt.Sprintf("The endpoint %s %s is not part of the Hive API.", r.Method, r.URL.Path),
					&code,
				)

			default:
				// StatusUnknown or any other value
				code := "unknown_endpoint"
				apierrors.WriteError(w, http.StatusNotFound,
					"invalid_request_error",
					fmt.Sprintf("Unknown endpoint: %s %s", r.Method, r.URL.Path),
					&code,
				)
			}
		})
	}
}
