package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all environment-sourced configuration for the control-plane.
type Config struct {
	Port            int
	SupabaseURL     string
	SupabaseAnonKey string
	SupabaseDBURL   string
	RedisURL        string

	// InternalToken is the shared secret required on /internal/* service-to-service
	// requests (issue #108). Empty leaves those endpoints unauthenticated (dev only);
	// the control-plane logs a warning at startup in that case.
	InternalToken string

	// Phase 15 — local batch executor knobs.
	BatchExecutorConcurrency   int
	BatchExecutorMaxRetries    int
	BatchExecutorLineTimeoutMs int
	BatchExecutorKind          string // "auto" | "local" | "upstream"

	// Signup abuse-prevention knobs (issue #116).
	//
	// SignupRateLimitPerWindow is the max signups allowed per client IP per
	// window (<=0 disables the IP limiter). SignupRateLimitWindow is the window
	// length. SignupRateLimitFailOpen mirrors the edge #51 policy: when the
	// Redis backend is unreachable the limiter denies by default (fail closed);
	// set RATE_LIMIT_FAIL_OPEN=true in dev only to admit instead.
	//
	// TurnstileSecretKey enables server-side Cloudflare Turnstile verification
	// when non-empty; empty disables CAPTCHA with a startup warning.
	//
	// TrustedProxyCIDRs is the list of CIDR ranges whose direct peers are
	// permitted to supply accurate CF-Connecting-IP / X-Forwarded-For headers.
	// Default (empty): forwarded headers are never trusted; the raw RemoteAddr
	// is always used. Set to Cloudflare IP ranges in production deployments
	// via TRUSTED_PROXY_CIDRS (comma-separated CIDR notation).
	//
	// PrecheckMaxConcurrent is the global concurrent-request ceiling for the
	// precheck handler (default 100). PrecheckTimeoutSeconds is the per-request
	// deadline in seconds (default 8).
	//
	// TenantProvisionRateLimitPerWindow and TenantProvisionRateLimitWindow
	// throttle POST /api/v1/viewer/tenant-provision per authenticated user id.
	// That route is a write path reachable by a principal holding no tenant
	// claim, which is the one thing such a token can do, so it must not be
	// hammerable even though every call is idempotent. The default of 20 per
	// ten minutes is far above the one call per session the console makes and
	// still leaves room for a user retrying after an administrator invites
	// them. Shares RATE_LIMIT_FAIL_OPEN with the signup limiter.
	SignupRateLimitPerWindow          int
	SignupRateLimitWindow             time.Duration
	SignupRateLimitFailOpen           bool
	TenantProvisionRateLimitPerWindow int
	TenantProvisionRateLimitWindow    time.Duration
	TurnstileSecretKey                string
	TrustedProxyCIDRs                 []*net.IPNet
	PrecheckMaxConcurrent             int
	PrecheckTimeoutSeconds            int

	// Licensing entitlement seam (issue #304, D9). LicenseFilePath set means
	// Hive Enterprise mode: an offline signed license file is read from this
	// path and verified with LicensePublicKeyB64 (a base64 std-encoded
	// Ed25519 public key). LicenseFilePath empty means Hive Cloud mode: the
	// sync-path placeholder (licensing.CloudSource) is wired instead.
	// LicenseRevalidateIntervalSeconds is how often the license is
	// re-validated ("validated locally on a schedule", the NVIDIA Delegated
	// License Server pattern -- no phone-home).
	LicenseFilePath                  string
	LicensePublicKeyB64              string
	LicenseRevalidateIntervalSeconds int
}

// IsEnterprisePosture derives Hive's deployment posture (issue #304 D9, issue
// #625, issue #653) from a LICENSE_FILE_PATH value: non-empty means Hive
// Enterprise (customer-hosted, membership administered, no self-serve
// personal tenants); empty means Hive Cloud (hosted SaaS, self-serve personal
// tenants on signup). This is the single source of truth for that branch:
// cmd/server derives signup.WebhookDeps.SelfServeTenants from it and
// cmd/backfill-tenants refuses to run against an Enterprise database because
// of it. Before issue #653 both entry points wrote out the same `!= ""` check
// independently, which could only stay in sync by coincidence. The predicate
// is total over all string inputs (empty or not), so there is no unrecognized
// value for this particular flag; callers that need a distinct fail-closed
// error for a malformed license (e.g. LICENSE_FILE_PATH set without
// LICENSE_PUBLIC_KEY) get that from Load, not from this function.
func IsEnterprisePosture(licenseFilePath string) bool {
	return licenseFilePath != ""
}

// Load reads configuration from environment variables and returns a validated Config.
func Load() (*Config, error) {
	portStr := os.Getenv("CONTROL_PLANE_PORT")
	if portStr == "" {
		portStr = "8081"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid CONTROL_PLANE_PORT %q: %w", portStr, err)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return nil, fmt.Errorf("SUPABASE_URL is required")
	}

	trustedCIDRs, err := parseCIDRList(os.Getenv("TRUSTED_PROXY_CIDRS"))
	if err != nil {
		return nil, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS: %w", err)
	}

	licenseFilePath := os.Getenv("LICENSE_FILE_PATH")
	licensePublicKeyB64 := os.Getenv("LICENSE_PUBLIC_KEY")
	if licenseFilePath != "" && licensePublicKeyB64 == "" {
		return nil, fmt.Errorf("LICENSE_PUBLIC_KEY is required when LICENSE_FILE_PATH is set")
	}

	return &Config{
		Port:                              port,
		SupabaseURL:                       supabaseURL,
		SupabaseAnonKey:                   os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseDBURL:                     os.Getenv("SUPABASE_DB_URL"),
		RedisURL:                          os.Getenv("REDIS_URL"),
		InternalToken:                     os.Getenv("CONTROL_PLANE_INTERNAL_TOKEN"),
		BatchExecutorConcurrency:          intEnv("BATCH_EXECUTOR_CONCURRENCY", 8),
		BatchExecutorMaxRetries:           intEnv("BATCH_EXECUTOR_MAX_RETRIES", 3),
		BatchExecutorLineTimeoutMs:        intEnv("BATCH_EXECUTOR_LINE_TIMEOUT_MS", 60000),
		BatchExecutorKind:                 stringEnv("BATCH_EXECUTOR_KIND", "auto"),
		SignupRateLimitPerWindow:          intEnv("SIGNUP_RATE_LIMIT_PER_WINDOW", 5),
		SignupRateLimitWindow:             time.Duration(intEnv("SIGNUP_RATE_LIMIT_WINDOW_SECONDS", 3600)) * time.Second,
		SignupRateLimitFailOpen:           boolEnv("RATE_LIMIT_FAIL_OPEN", false),
		TenantProvisionRateLimitPerWindow: intEnv("TENANT_PROVISION_RATE_LIMIT_PER_WINDOW", 20),
		TenantProvisionRateLimitWindow:    time.Duration(intEnv("TENANT_PROVISION_RATE_LIMIT_WINDOW_SECONDS", 600)) * time.Second,
		TurnstileSecretKey:                os.Getenv("TURNSTILE_SECRET_KEY"),
		TrustedProxyCIDRs:                 trustedCIDRs,
		PrecheckMaxConcurrent:             intEnv("SIGNUP_PRECHECK_MAX_CONCURRENT", 100),
		PrecheckTimeoutSeconds:            intEnv("SIGNUP_PRECHECK_TIMEOUT_SECONDS", 8),
		LicenseFilePath:                   licenseFilePath,
		LicensePublicKeyB64:               licensePublicKeyB64,
		LicenseRevalidateIntervalSeconds:  intEnv("LICENSE_REVALIDATE_INTERVAL_SECONDS", 300),
	}, nil
}

// parseCIDRList parses a comma-separated list of CIDR strings. Empty string
// returns a nil slice (no trusted proxies). Malformed entries are returned as
// an error so misconfiguration is caught at startup.
func parseCIDRList(raw string) ([]*net.IPNet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", p, err)
		}
		out = append(out, cidr)
	}
	return out, nil
}

func intEnv(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func stringEnv(key, def string) string {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	return v
}

func boolEnv(key string, def bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
