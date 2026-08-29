package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinkconfig"
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
	t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
	t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
	t.Setenv("LANGFUSE_SECRET_KEY", "sec")

	// All ENABLE_* flags absent — expect zero sinks regardless of credentials.
	sinks, _ := configuredAuditSinks()
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
		{
			name: "langfuse", envKey: "ENABLE_AUDIT_SINK_LANGFUSE",
			credKey: "LANGFUSE_HOST", credVal: "http://langfuse.example.com",
			sinkName: "langfuse",
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
			// Langfuse requires host + both keys; supply the extras here.
			if tc.name == "langfuse" {
				t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
				t.Setenv("LANGFUSE_SECRET_KEY", "sec")
			}

			result, _ := configuredAuditSinks()
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

	sinks, _ := configuredAuditSinks()
	assert.Empty(t, sinks, "sink must be skipped when enable flag is set but credentials are absent")
}

// TestConfiguredAuditSinksPartialCredsSkipped verifies that sinks with
// multi-credential requirements are skipped when only some credentials are set.
func TestConfiguredAuditSinksPartialCredsSkipped(t *testing.T) {
	t.Run("splunk_url_only", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_SPLUNK", "true")
		t.Setenv("AUDIT_SINK_SPLUNK_HEC_URL", "http://splunk.example.com")
		// token intentionally absent
		result, _ := configuredAuditSinks()
		assert.Empty(t, result, "splunk must be skipped when HEC token is absent")
	})
	t.Run("splunk_token_only", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_SPLUNK", "true")
		t.Setenv("AUDIT_SINK_SPLUNK_HEC_TOKEN", "tok")
		// url intentionally absent
		result, _ := configuredAuditSinks()
		assert.Empty(t, result, "splunk must be skipped when HEC url is absent")
	})
	t.Run("langfuse_host_only", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_LANGFUSE", "true")
		t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
		// public and secret keys intentionally absent
		result, _ := configuredAuditSinks()
		assert.Empty(t, result, "langfuse must be skipped when keys are absent")
	})
	t.Run("langfuse_missing_secret", func(t *testing.T) {
		t.Setenv("ENABLE_AUDIT_SINK_LANGFUSE", "true")
		t.Setenv("LANGFUSE_HOST", "http://langfuse.example.com")
		t.Setenv("LANGFUSE_PUBLIC_KEY", "pub")
		// secret key intentionally absent
		result, _ := configuredAuditSinks()
		assert.Empty(t, result, "langfuse must be skipped when secret key is absent")
	})
}

// TestConfiguredAuditSinksDelegatesWithoutAddingInputs pins the delegate to
// the package that owns the decision. The compliance suite proves, against a
// real database and a real receiver, that sinkconfig.FromEnv takes no input
// but the environment; that proof is worth nothing if the wrapper main
// actually calls can grow a second input of its own. A per-tenant resolver
// call added here, or a sink appended between here and auditworker.New, would
// leave the compliance suite green and this assertion red.
func TestConfiguredAuditSinksDelegatesWithoutAddingInputs(t *testing.T) {
	t.Setenv("ENABLE_AUDIT_SINK_ELK", "true")
	t.Setenv("AUDIT_SINK_ELK_URL", "http://elk.example.com")
	t.Setenv("ENABLE_AUDIT_SINK_SENTRY", "true")
	// SENTRY_DSN intentionally absent, so one sink registers and one is skipped
	// and both halves of the return value carry something to compare.

	gotSinks, gotSkipped := configuredAuditSinks()
	wantSinks, wantSkipped := sinkconfig.FromEnv()

	require.Len(t, gotSinks, len(wantSinks))
	for i := range gotSinks {
		assert.Equal(t, wantSinks[i].Name(), gotSinks[i].Name())
	}
	assert.Equal(t, wantSkipped, gotSkipped)
	assert.Equal(t, []string{"sentry"}, gotSkipped,
		"a sink whose flag is true and whose credentials are missing must be reported, not dropped")
}

// The case-insensitive flag parsing this file used to test directly moved to
// internal/auditworker/sinkconfig along with the helper itself (issue #755),
// and its test moved with it, to that package's sinkconfig_test.go. The tests
// above stay here because configuredAuditSinks is what main wires into the
// worker, and that wiring is the thing that must not regress.
