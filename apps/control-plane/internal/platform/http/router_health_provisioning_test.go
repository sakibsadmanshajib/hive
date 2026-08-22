package http

// The signup-provisioning contribution to the readiness endpoint (D-023).
//
// Tenant provisioning used to be driven by a Supabase Database Webhook created
// in a dashboard. Deleting the hosted project removes it with no diff, no error
// and no failing test. The control-plane wiring that replaced it then sat
// behind an env-var check that logged a warning and started healthy anyway, so
// the live demo box ran with no reachable provisioning path and a green
// healthcheck. These tests exist so that the next absence is a red container
// healthcheck and a blocked deploy instead of a log line.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

const healthPath = "/health"

func getHealth(t *testing.T, cfg RouterConfig) (int, healthResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	NewRouter(cfg).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, healthPath, nil))
	var body healthResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&body))
	return rec.Code, body
}

// A nil reporter is the shape a regression takes: somebody stops wiring
// provisioning in cmd/server and every unit test still passes. It must not read
// as healthy.
func TestHealthIsDegradedWhenProvisioningIsNotReported(t *testing.T) {
	code, body := getHealth(t, RouterConfig{DBReady: true})

	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, healthStatusDegraded, body.Status)
	require.Equal(t, provisioningUnreported, body.Reason)
}

const sweepFailingReason = "signup provisioning sweep failing"

// Wired but broken: the reconciler reports its own failure, and the endpoint
// carries it through rather than turning it into silence.
func TestHealthIsDegradedWhenProvisioningReportsFailure(t *testing.T) {
	reporter := func() (bool, string) {
		return false, sweepFailingReason
	}
	code, body := getHealth(t, RouterConfig{DBReady: true, ProvisioningReady: reporter})

	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, healthStatusDegraded, body.Status)
	require.Equal(t, sweepFailingReason, body.Reason)
}

// The counterpart, so the two tests above cannot pass on a router that
// degrades unconditionally.
func TestHealthIsOkWhenProvisioningIsReady(t *testing.T) {
	var noReason string
	reporter := func() (bool, string) {
		return true, noReason
	}
	code, body := getHealth(t, RouterConfig{DBReady: true, ProvisioningReady: reporter})

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, healthStatusOK, body.Status)
	require.Empty(t, body.Reason)
}
