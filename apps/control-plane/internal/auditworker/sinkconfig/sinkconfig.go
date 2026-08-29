// Package sinkconfig builds the audit fan-out sink set from the process
// environment. It is the single place the enablement decision is made.
//
// Why this is a package rather than a function in cmd/server (issue #755):
// the decision is a security boundary — it is what starts, or refuses to
// start, outbound egress of audit records to a third party — and a security
// boundary that only `package main` can reach cannot be tested by anything
// that also needs a database. The database-backed proof in
// apps/control-plane/tests/compliance/audit_sink_enablement_test.go calls
// FromEnv directly, so the thing under test is the constructor production
// runs and not a copy of it that can drift.
//
// Scope, decided in issue #755: audit sink enablement is DEPLOYMENT
// configuration, not tenant configuration. There is deliberately no
// database input to this function, and adding one would be a regression:
//
//   - The credentials are the operator's and the sink set is process-global.
//     A per-tenant switch over an operator-owned egress has no coherent
//     meaning on a shared deployment.
//   - A tenant-scoped switch over the operator's own audit export is an
//     audit-evasion control: a workspace could suppress export of its own
//     trail to the operator's SIEM. On a product sold on audit posture that
//     is not a feature with a caveat.
//   - Enablement and credentials belong in one store. Splitting them across
//     a reviewed, version-controlled .env and an unversioned runtime row is
//     the exact failure mode D-044 was written about.
//
// The six ENABLE_AUDIT_SINK_* rows were removed from public.feature_gate_keys
// by supabase/migrations/20260829_03_retire_audit_sink_feature_gates.sql, so
// no console control claims otherwise.
package sinkconfig

import (
	"log"
	"os"
	"strings"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auditworker/sinks"
)

// enabled reports whether the explicit opt-in environment variable for the
// named sink is set to "true". Credential presence alone is not sufficient:
// on the sovereign enterprise profile all external egress is off by default
// and must be consciously enabled by the operator who runs the deployment.
//
// The variable names match the (now retired) public.tenant_setting_key enum
// labels only as a historical accident of vocabulary. They are not two ways
// to reach one control: the environment is the whole decision, and no
// database value narrows or widens it.
func enabled(key string) bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(key)), "true")
}

// FromEnv returns the sinks this process should fan audit records out to.
// Each sink requires BOTH an explicit enable flag AND valid credentials. The
// enable flags default to absent (off), making every external sink opt-in.
// This satisfies the sovereign-edge zero-egress promise.
func FromEnv() []sinks.Sink {
	configured := make([]sinks.Sink, 0, 6)
	if enabled("ENABLE_AUDIT_SINK_ELK") {
		if url := strings.TrimSpace(os.Getenv("AUDIT_SINK_ELK_URL")); url != "" {
			configured = append(configured, sinks.NewELK(sinks.ELKConfig{
				URL:    url,
				APIKey: strings.TrimSpace(os.Getenv("AUDIT_SINK_ELK_API_KEY")),
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_ELK=true but AUDIT_SINK_ELK_URL is unset — sink skipped")
		}
	}
	if enabled("ENABLE_AUDIT_SINK_LOKI") {
		if url := strings.TrimSpace(os.Getenv("AUDIT_SINK_LOKI_URL")); url != "" {
			configured = append(configured, sinks.NewLoki(sinks.LokiConfig{URL: url}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_LOKI=true but AUDIT_SINK_LOKI_URL is unset — sink skipped")
		}
	}
	if enabled("ENABLE_AUDIT_SINK_DATADOG") {
		if key := strings.TrimSpace(os.Getenv("AUDIT_SINK_DATADOG_API_KEY")); key != "" {
			configured = append(configured, sinks.NewDatadog(sinks.DatadogConfig{
				APIKey: key,
				Site:   strings.TrimSpace(os.Getenv("AUDIT_SINK_DATADOG_SITE")),
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_DATADOG=true but AUDIT_SINK_DATADOG_API_KEY is unset — sink skipped")
		}
	}
	if enabled("ENABLE_AUDIT_SINK_SPLUNK") {
		url := strings.TrimSpace(os.Getenv("AUDIT_SINK_SPLUNK_HEC_URL"))
		token := strings.TrimSpace(os.Getenv("AUDIT_SINK_SPLUNK_HEC_TOKEN"))
		if url != "" && token != "" {
			configured = append(configured, sinks.NewSplunk(sinks.SplunkConfig{
				URL:   url,
				Token: token,
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_SPLUNK=true but AUDIT_SINK_SPLUNK_HEC_URL or AUDIT_SINK_SPLUNK_HEC_TOKEN is unset — sink skipped")
		}
	}
	if enabled("ENABLE_AUDIT_SINK_SENTRY") {
		if dsn := strings.TrimSpace(os.Getenv("SENTRY_DSN")); dsn != "" {
			configured = append(configured, sinks.NewSentry(sinks.SentryConfig{DSN: dsn}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_SENTRY=true but SENTRY_DSN is unset — sink skipped")
		}
	}
	if enabled("ENABLE_AUDIT_SINK_LANGFUSE") {
		host := strings.TrimSpace(os.Getenv("LANGFUSE_HOST"))
		pub := strings.TrimSpace(os.Getenv("LANGFUSE_PUBLIC_KEY"))
		sec := strings.TrimSpace(os.Getenv("LANGFUSE_SECRET_KEY"))
		if host != "" && pub != "" && sec != "" {
			configured = append(configured, sinks.NewLangfuse(sinks.LangfuseConfig{
				Host:      host,
				PublicKey: pub,
				SecretKey: sec,
			}))
		} else {
			log.Println("WARNING: ENABLE_AUDIT_SINK_LANGFUSE=true but LANGFUSE_HOST, LANGFUSE_PUBLIC_KEY, or LANGFUSE_SECRET_KEY is unset — sink skipped")
		}
	}
	return configured
}
