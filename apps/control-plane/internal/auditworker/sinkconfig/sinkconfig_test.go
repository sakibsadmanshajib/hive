package sinkconfig

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEnabledRequiresLiteralTrue pins the flag parsing that decides whether
// outbound egress starts. Anything other than a case-insensitive "true" is
// off, so a half-configured deployment (a stray "1", "yes", "on") stays at
// the zero-egress default rather than guessing the operator meant yes.
// Moved here from apps/control-plane/cmd/server/audit_sinks_gate_test.go with
// the helper itself (issue #755).
func TestEnabledRequiresLiteralTrue(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
	assert.True(t, enabled("ENABLE_AUDIT_SINK_ELK"))

	t.Setenv("ENABLE_AUDIT_SINK_LOKI", "TRUE")
	assert.True(t, enabled("ENABLE_AUDIT_SINK_LOKI"))

	t.Setenv("ENABLE_AUDIT_SINK_SENTRY", "  true  ")
	assert.True(t, enabled("ENABLE_AUDIT_SINK_SENTRY"), "surrounding whitespace must not defeat the flag")

	t.Setenv("ENABLE_AUDIT_SINK_DATADOG", "1")
	assert.False(t, enabled("ENABLE_AUDIT_SINK_DATADOG"), "only 'true' (case-insensitive) must enable")

	t.Setenv("ENABLE_AUDIT_SINK_LANGFUSE", "yes")
	assert.False(t, enabled("ENABLE_AUDIT_SINK_LANGFUSE"), "only 'true' (case-insensitive) must enable")

	assert.False(t, enabled("ENABLE_AUDIT_SINK_SPLUNK"), "absent var must return false")
}

// TestFromEnvIgnoresEverythingButTheEnvironment is the structural half of the
// issue #755 guarantee: FromEnv takes no arguments and reaches no store, so
// there is no seam through which a tenant-scoped value could narrow or widen
// the egress decision. The behavioural half, against a real database and a
// real receiver, is in apps/control-plane/tests/compliance.
func TestFromEnvIgnoresEverythingButTheEnvironment(t *testing.T) {
	t.Setenv("AUDIT_SINK_ELK_URL", "http://elk.example.com")
	t.Setenv("AUDIT_SINK_LOKI_URL", "http://loki.example.com")
	t.Setenv("AUDIT_SINK_DATADOG_API_KEY", "dd-key")
	t.Setenv("AUDIT_SINK_SPLUNK_HEC_URL", "http://splunk.example.com")
	t.Setenv("AUDIT_SINK_SPLUNK_HEC_TOKEN", "splunk-token")
	t.Setenv("SENTRY_DSN", "https://key@sentry.example.com/1")
	t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
	t.Setenv("LANGFUSE_SECRET_KEY", "sec")

	none, skipped := FromEnv()
	assert.Empty(t, none, "credentials alone must never start egress")
	assert.Empty(t, skipped, "an absent flag is not a skipped sink, it is a sink nobody asked for")

	t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
	got, skipped := FromEnv()
	assert.Len(t, got, 1)
	assert.Equal(t, "elk", got[0].Name())
	assert.Empty(t, skipped)
}

// TestFromEnvReportsRequestedButUnconfigurableSinks is the loud-failure half.
// A flag set to true with no credentials used to produce one WARNING line and
// then a summary line reading "no optional sinks configured", which is false
// and sends the next reader looking in the wrong place. The second return
// value is what lets the caller say which sink the operator asked for and did
// not get.
func TestFromEnvReportsRequestedButUnconfigurableSinks(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_SPLUNK", "true")
	t.Setenv("AUDIT_SINK_SPLUNK_HEC_URL", "http://splunk.example.com")
	// HEC token intentionally absent: splunk needs both.
	t.Setenv("ENABLE_AUDIT_SINK_LANGFUSE", "true")
	// Every langfuse credential intentionally absent.
	t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
	t.Setenv("AUDIT_SINK_ELK_URL", "http://elk.example.com")

	configured, skipped := FromEnv()

	assert.Len(t, configured, 1, "the one fully configured sink still registers")
	assert.Equal(t, "elk", configured[0].Name())
	assert.Equal(t, []string{"splunk", "langfuse"}, skipped)
}
