package config

import (
	"testing"
	"time"
)

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
