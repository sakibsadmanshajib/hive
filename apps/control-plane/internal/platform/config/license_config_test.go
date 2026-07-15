package config

import "testing"

func TestLicenseDefaults(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	// Leave all LICENSE_* envs unset: cloud mode (no file configured).
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LicenseFilePath != "" {
		t.Fatalf("default LicenseFilePath should be empty, got %q", cfg.LicenseFilePath)
	}
	if cfg.LicenseRevalidateIntervalSeconds != 300 {
		t.Fatalf("default LicenseRevalidateIntervalSeconds = %d, want 300", cfg.LicenseRevalidateIntervalSeconds)
	}
}

func TestLicenseFilePathRequiresPublicKey(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("LICENSE_FILE_PATH", "/etc/hive/license.json")
	t.Setenv("LICENSE_PUBLIC_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when LICENSE_FILE_PATH is set without LICENSE_PUBLIC_KEY")
	}
}

func TestLicenseFilePathWithPublicKeyLoads(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("LICENSE_FILE_PATH", "/etc/hive/license.json")
	t.Setenv("LICENSE_PUBLIC_KEY", "c29tZS1rZXktYnl0ZXM=")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LicenseFilePath != "/etc/hive/license.json" {
		t.Fatalf("LicenseFilePath = %q", cfg.LicenseFilePath)
	}
	if cfg.LicensePublicKeyB64 != "c29tZS1rZXktYnl0ZXM=" {
		t.Fatalf("LicensePublicKeyB64 = %q", cfg.LicensePublicKeyB64)
	}
}

func TestLicenseRevalidateIntervalOverride(t *testing.T) {
	t.Setenv("SUPABASE_URL", "https://example.supabase.co")
	t.Setenv("LICENSE_REVALIDATE_INTERVAL_SECONDS", "60")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.LicenseRevalidateIntervalSeconds != 60 {
		t.Fatalf("LicenseRevalidateIntervalSeconds = %d, want 60", cfg.LicenseRevalidateIntervalSeconds)
	}
}
