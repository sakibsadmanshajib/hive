package config

import (
	"testing"
	"time"
)

func TestTrustedProxyCIDRsDefault(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Fatalf("default TrustedProxyCIDRs should be empty, got %v", cfg.TrustedProxyCIDRs)
	}
}

func TestTrustedProxyCIDRsParsed(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.0/8,192.168.0.0/16")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("expected 2 CIDRs, got %d: %v", len(cfg.TrustedProxyCIDRs), cfg.TrustedProxyCIDRs)
	}
}

func TestTrustedProxyCIDRsInvalid(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
	_, err := Load()
	if err == nil {
		t.Fatal("expected error for invalid CIDR, got nil")
	}
}

func TestPrecheckConcurrencyAndTimeoutDefaults(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.PrecheckMaxConcurrent != 100 {
		t.Fatalf("PrecheckMaxConcurrent default = %d, want 100", cfg.PrecheckMaxConcurrent)
	}
	if cfg.PrecheckTimeoutSeconds != 8 {
		t.Fatalf("PrecheckTimeoutSeconds default = %d, want 8", cfg.PrecheckTimeoutSeconds)
	}
}

func TestSignupGuardDefaults(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	// Leave all signup-guard envs unset to assert defaults.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SignupRateLimitPerWindow != 5 {
		t.Fatalf("default per-window = %d, want 5", cfg.SignupRateLimitPerWindow)
	}
	if cfg.SignupRateLimitWindow != time.Hour {
		t.Fatalf("default window = %s, want 1h", cfg.SignupRateLimitWindow)
	}
	if cfg.SignupRateLimitFailOpen {
		t.Fatal("rate limiter must default to fail-closed")
	}
	if cfg.TurnstileSecretKey != "" {
		t.Fatal("turnstile secret should be empty by default (feature disabled)")
	}
}

func TestSignupGuardOverrides(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("SIGNUP_RATE_LIMIT_PER_WINDOW", "20")
	t.Setenv("SIGNUP_RATE_LIMIT_WINDOW_SECONDS", "600")
	t.Setenv("RATE_LIMIT_FAIL_OPEN", "true")
	t.Setenv("TURNSTILE_SECRET_KEY", "a-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SignupRateLimitPerWindow != 20 {
		t.Fatalf("per-window = %d, want 20", cfg.SignupRateLimitPerWindow)
	}
	if cfg.SignupRateLimitWindow != 10*time.Minute {
		t.Fatalf("window = %s, want 10m", cfg.SignupRateLimitWindow)
	}
	if !cfg.SignupRateLimitFailOpen {
		t.Fatal("fail-open override not applied")
	}
	if cfg.TurnstileSecretKey != "a-secret" {
		t.Fatalf("turnstile secret = %q, want a-secret", cfg.TurnstileSecretKey)
	}
}
