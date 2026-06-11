package config

import (
	"fmt"
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
	BatchExecutorConcurrency  int
	BatchExecutorMaxRetries   int
	BatchExecutorLineTimeoutMs int
	BatchExecutorKind         string // "auto" | "local" | "upstream"

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
	SignupRateLimitPerWindow int
	SignupRateLimitWindow    time.Duration
	SignupRateLimitFailOpen  bool
	TurnstileSecretKey       string
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

	return &Config{
		Port:                       port,
		SupabaseURL:                supabaseURL,
		SupabaseAnonKey:            os.Getenv("SUPABASE_ANON_KEY"),
		SupabaseDBURL:              os.Getenv("SUPABASE_DB_URL"),
		RedisURL:                   os.Getenv("REDIS_URL"),
		InternalToken:              os.Getenv("CONTROL_PLANE_INTERNAL_TOKEN"),
		BatchExecutorConcurrency:   intEnv("BATCH_EXECUTOR_CONCURRENCY", 8),
		BatchExecutorMaxRetries:    intEnv("BATCH_EXECUTOR_MAX_RETRIES", 3),
		BatchExecutorLineTimeoutMs: intEnv("BATCH_EXECUTOR_LINE_TIMEOUT_MS", 60000),
		BatchExecutorKind:          stringEnv("BATCH_EXECUTOR_KIND", "auto"),
		SignupRateLimitPerWindow:   intEnv("SIGNUP_RATE_LIMIT_PER_WINDOW", 5),
		SignupRateLimitWindow:      time.Duration(intEnv("SIGNUP_RATE_LIMIT_WINDOW_SECONDS", 3600)) * time.Second,
		SignupRateLimitFailOpen:    boolEnv("RATE_LIMIT_FAIL_OPEN", false),
		TurnstileSecretKey:         os.Getenv("TURNSTILE_SECRET_KEY"),
	}, nil
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
