package metrics_test

// Coverage for the two collectors that make a permanently failed signup
// provisioning visible. The gauge they sit next to,
// hive_signup_provisioning_sweep_failures, resets on any clean sweep, and a
// sweep goes clean the moment the identity that kept faulting ages out of the
// reconciler's lookback window, so an alert on that gauge alone resolves itself
// at the exact instant the loss becomes permanent (review finding on PR 993).
//
// The assertions are on the exported series rather than on the accessors: a
// metric that is registered but never reaches /metrics is the same invisibility
// wearing a different hat.

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/metrics"
)

const (
	faultsSeries   = "hive_signup_provisioning_faults_total"
	strandedSeries = "hive_signup_provisioning_stranded_identities"
)

// fakeSource stands in for *signup.Reconciler. Plain mutable fields, because
// what is under test is that the collectors read the source on every scrape
// rather than snapshotting it once at registration.
type fakeSource struct {
	faults   int
	stranded int
}

func (f *fakeSource) Faults() int             { return f.faults }
func (f *fakeSource) StrandedIdentities() int { return f.stranded }

func TestRegisterSignupProvisioningExportsBothSeries(t *testing.T) {
	src := &fakeSource{}
	reg := prometheus.NewRegistry()
	require.NoError(t, metrics.RegisterSignupProvisioning(reg, src))

	value, kind := scrape(t, reg, faultsSeries)
	require.Zero(t, value)
	// COUNTER, not GAUGE, so increase() and rate() are valid on it and a process
	// restart reads as a counter reset instead of a drop to be alerted on. The
	// alert in deploy/prometheus/alerts.yml depends on this.
	require.Equal(t, "COUNTER", kind)

	_, kind = scrape(t, reg, strandedSeries)
	require.Equal(t, "GAUGE", kind)

	// Every scrape must see the current value. A collector that captured the
	// count at registration would sit at zero forever and the alert would never
	// fire, which is the failure mode being fixed here.
	src.faults = 3
	src.stranded = 2
	value, _ = scrape(t, reg, faultsSeries)
	require.EqualValues(t, 3, value)
	value, _ = scrape(t, reg, strandedSeries)
	require.EqualValues(t, 2, value)
}

func TestRegisterSignupProvisioningRefusesMissingDependencies(t *testing.T) {
	require.Error(t, metrics.RegisterSignupProvisioning(nil, &fakeSource{}))
	require.Error(t, metrics.RegisterSignupProvisioning(prometheus.NewRegistry(), nil))

	// A duplicate registration has to surface rather than be swallowed: a caller
	// that silently lost its collectors is back to having no signal at all.
	reg := prometheus.NewRegistry()
	require.NoError(t, metrics.RegisterSignupProvisioning(reg, &fakeSource{}))
	require.Error(t, metrics.RegisterSignupProvisioning(reg, &fakeSource{}))
}

// scrape reads one series out of a real Gather, so the assertion covers the path
// an actual /metrics request takes rather than calling the closure directly.
func scrape(t *testing.T, reg *prometheus.Registry, name string) (float64, string) {
	t.Helper()
	families, err := reg.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		require.Len(t, f.GetMetric(), 1)
		m := f.GetMetric()[0]
		if c := m.GetCounter(); c != nil {
			return c.GetValue(), f.GetType().String()
		}
		return m.GetGauge().GetValue(), f.GetType().String()
	}
	t.Fatalf("series %s was not exported by the registry", name)
	return 0, ""
}
