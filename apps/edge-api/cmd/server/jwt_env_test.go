package main

import (
	"testing"
)

// The https-only rule on SUPABASE_JWKS_URL is the reason the self-hosted
// profile needs a TLS front for GoTrue at all. It is load bearing: an http
// JWKS URL lets anything on the path swap the key set and mint tokens this
// service would accept. These tests exist so a future "just make enterprise
// work" change cannot quietly relax it.

func TestLoadJWTAuthEnv_RejectsPlainHTTPJWKS(t *testing.T) {
	t.Setenv("SUPABASE_JWT_ISSUER", "http://supabase-auth:9999")
	t.Setenv("SUPABASE_JWT_AUDIENCE", "authenticated")
	t.Setenv("SUPABASE_JWKS_URL", "http://supabase-auth:9999/.well-known/jwks.json")

	if _, err := loadJWTAuthEnv(); err == nil {
		t.Fatal("expected a plain http JWKS URL to be rejected")
	}
}

func TestLoadJWTAuthEnv_CAFileIsOptionalAndCarried(t *testing.T) {
	t.Setenv("SUPABASE_JWT_ISSUER", "https://auth.example/auth/v1")
	t.Setenv("SUPABASE_JWT_AUDIENCE", "authenticated")
	t.Setenv("SUPABASE_JWKS_URL", "https://auth.example/auth/v1/.well-known/jwks.json")
	t.Setenv("SUPABASE_JWKS_CA_FILE", "")

	cfg, err := loadJWTAuthEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CAFile != "" {
		t.Fatalf("expected no CA file, got %q", cfg.CAFile)
	}

	t.Setenv("SUPABASE_JWKS_CA_FILE", "  /etc/hive/jwks-ca.pem  ")
	cfg, err = loadJWTAuthEnv()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.CAFile != "/etc/hive/jwks-ca.pem" {
		t.Fatalf("expected the trimmed CA path, got %q", cfg.CAFile)
	}
}

func TestLoadJWTAuthEnv_CAFileDoesNotExcuseHTTP(t *testing.T) {
	t.Setenv("SUPABASE_JWT_ISSUER", "http://supabase-auth:9999")
	t.Setenv("SUPABASE_JWT_AUDIENCE", "authenticated")
	t.Setenv("SUPABASE_JWKS_URL", "http://supabase-auth:9999/.well-known/jwks.json")
	t.Setenv("SUPABASE_JWKS_CA_FILE", "/etc/hive/jwks-ca.pem")

	if _, err := loadJWTAuthEnv(); err == nil {
		t.Fatal("a CA file must not make a plain http JWKS URL acceptable")
	}
}
