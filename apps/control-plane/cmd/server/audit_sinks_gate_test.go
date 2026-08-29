package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfiguredAuditSinksDefaultOff verifies that no external sink is
// registered when the ENABLE_AUDIT_SINK_* environment variables are absent.
// This is the sovereign-edge zero-egress guarantee: credential presence alone
// must not cause egress.
func TestConfiguredAuditSinksDefaultOff(t *testing.T) {
	// Ensure all sink credential vars are set but enable flags are absent.
	t.Setenv("AUDIT_SINK_ELK_URL", "http://elk.example.com")
	t.Setenv("AUDIT_SINK_ELK_API_KEY", "elk-key")
	t.Setenv("AUDIT_SINK_LOKI_URL", "http://loki.example.com")
	t.Setenv("AUDIT_SINK_DATADOG_API_KEY", "dd-key")
	t.Setenv("AUDIT_SINK_SPLUNK_HEC_URL", "http://splunk.example.com")
	t.Setenv("AUDIT_SINK_SPLUNK_HEC_TOKEN", "splunk-token")
	t.Setenv("SENTRY_DSN", "https://key@sentry.example.com/1")

	// All ENABLE_* flags absent — expect zero sinks regardless of credentials.
	sinks := configuredAuditSinks()
	assert.Empty(t, sinks, "no sink must be registered when ENABLE_AUDIT_SINK_* flags are absent")
}

// TestConfiguredAuditSinksEachFlagGates verifies that setting exactly one
// ENABLE flag registers exactly one sink.
func TestConfiguredAuditSinksEachFlagGates(t *testing.T) {
	cases := []struct {
		name     string
		envKey   string
		credKey  string
		credVal  string
		sinkName string
	}{
		{
			name: "elk", envKey: "ENABLE_AUDIT_SINK_ELK",
			credKey: "AUDIT_SINK_ELK_URL", credVal: "http://elk.example.com",
			sinkName: "elk",
		},
		{
			name: "loki", envKey: "ENABLE_AUDIT_SINK_LOKI",
			credKey: "AUDIT_SINK_LOKI_URL", credVal: "http://loki.example.com",
			sinkName: "loki",
		},
		{
			name: "datadog", envKey: "ENABLE_AUDIT_SINK_DATADOG",
			credKey: "AUDIT_SINK_DATADOG_API_KEY", credVal: "dd-key",
			sinkName: "datadog",
		},
		{
			name: "splunk", envKey: "ENABLE_AUDIT_SINK_SPLUNK",
			credKey: "AUDIT_SINK_SPLUNK_HEC_URL", credVal: "http://splunk.example.com",
			sinkName: "splunk",
		},
		{
			name: "sentry", envKey: "ENABLE_AUDIT_SINK_SENTRY",
			credKey: "SENTRY_DSN", credVal: "https://key@sentry.example.com/1",
			sinkName: "sentry",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, "true")
			t.Setenv(tc.credKey, tc.credVal)
			// Splunk requires both URL and token; supply the second cred here.
			if tc.name == "splunk" {
				t.Setenv("AUDIT_SINK_SPLUNK_HEC_TOKEN", "tok")
			}
			result := configuredAuditSinks()
			require.Len(t, result, 1, "exactly one sink must be registered when only %s=true", tc.envKey)
			assert.Equal(t, tc.sinkName, result[0].Name())
		})
	}
}

// TestConfiguredAuditSinksMissingCredSkipped verifies that setting an enable
// flag without credentials logs a warning and registers zero sinks (no panic,
// no partial registration).
func TestConfiguredAuditSinksMissingCredSkipped(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_SENTRY", "true")
	// SENTRY_DSN intentionally absent.

	sinks := configuredAuditSinks()
	assert.Empty(t, sinks, "sink must be skipped when enable flag is set but credentials are absent")
}

// TestConfiguredAuditSinksPartialCredsSkipped verifies that sinks with
// multi-credential requirements are skipped when only some credentials are set.
func TestConfiguredAuditSinksPartialCredsSkipped(t *testing.T) {
	t.Run("splunk_url_only", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_SPLUNK", "true")
		t.Setenv("AUDIT_SINK_SPLUNK_HEC_URL", "http://splunk.example.com")
		// token intentionally absent
		result := configuredAuditSinks()
		assert.Empty(t, result, "splunk must be skipped when HEC token is absent")
	})
	t.Run("splunk_token_only", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_SPLUNK", "true")
		t.Setenv("AUDIT_SINK_SPLUNK_HEC_TOKEN", "tok")
		// url intentionally absent
		result := configuredAuditSinks()
		assert.Empty(t, result, "splunk must be skipped when HEC url is absent")
	})
}

// TestConfiguredAuditSinksHasNoLangfuseSink is a deletion guard. The Langfuse
// audit sink was removed in favour of internal/genexport, which reads the
// accounting tables directly. Two Langfuse writers is worse than either, so if
// somebody reintroduces the sink this test says why not to.
func TestConfiguredAuditSinksHasNoLangfuseSink(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_LANGFUSE", "true")
	t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
	t.Setenv("LANGFUSE_SECRET_KEY", "sec")

	result := configuredAuditSinks()
	assert.Empty(t, result,
		"the langfuse audit sink is deleted; generation telemetry goes through internal/genexport")
}

// TestGenerationExporterDefaultOff is the ships-dark guarantee at the wiring
// level: credentials alone must never start an exporter.
func TestGenerationExporterDefaultOff(t *testing.T) {
	t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
	t.Setenv("LANGFUSE_SECRET_KEY", "sec")
	// ENABLE_GENERATION_EXPORT deliberately absent.

	assert.Nil(t, buildGenerationExporter(nil),
		"the exporter must not start on credential presence alone")
}

// TestGenerationExporterSkippedWhenIncomplete verifies that an enabled flag
// with any credential missing yields no exporter rather than a half-configured
// one. The boot WARNING that names the missing variable is emitted alongside.
func TestGenerationExporterSkippedWhenIncomplete(t *testing.T) {
	cases := map[string]map[string]string{
		"no host":        {"LANGFUSE_PUBLIC_KEY": "pub", "LANGFUSE_SECRET_KEY": "sec"},
		"no public key":  {"LANGFUSE_HOST": "http://langfuse.example.com", "LANGFUSE_SECRET_KEY": "sec"},
		"no secret key":  {"LANGFUSE_HOST": "http://langfuse.example.com", "LANGFUSE_PUBLIC_KEY": "pub"},
		"nothing at all": {},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ENABLE_GENERATION_EXPORT", "true")
			t.Setenv("LANGFUSE_HOST", "")
			t.Setenv("LANGFUSE_PUBLIC_KEY", "")
			t.Setenv("LANGFUSE_SECRET_KEY", "")
			for key, value := range env {
				t.Setenv(key, value)
			}
			assert.Nil(t, buildGenerationExporter(nil))
		})
	}
}

// TestGenerationExporterNeedsAPool: fully credentialed but with no database
// pool there is nothing to read, so the exporter is skipped with a named
// reason rather than started and left failing every tick.
func TestGenerationExporterNeedsAPool(t *testing.T) {
	t.Setenv("ENABLE_GENERATION_EXPORT", "true")
	t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
	t.Setenv("LANGFUSE_SECRET_KEY", "sec")

	assert.Nil(t, buildGenerationExporter(nil))
}

// TestAuditSinkEnabled verifies the helper's case-insensitive true detection.
func TestAuditSinkEnabled(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
	assert.True(t, auditSinkEnabled("ENABLE_AUDIT_SINK_ELK"))

	t.Setenv("ENABLE_AUDIT_SINK_LOKI", "TRUE")
	assert.True(t, auditSinkEnabled("ENABLE_AUDIT_SINK_LOKI"))

	t.Setenv("ENABLE_AUDIT_SINK_DATADOG", "1")
	assert.False(t, auditSinkEnabled("ENABLE_AUDIT_SINK_DATADOG"), "only 'true' (case-insensitive) must enable")

	assert.False(t, auditSinkEnabled("ENABLE_AUDIT_SINK_SPLUNK"), "absent var must return false")
}
