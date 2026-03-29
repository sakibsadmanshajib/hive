package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all environment-sourced configuration for the control-plane.
type Config struct {
	Port          int
	SupabaseURL   string
	SupabaseDBURL string
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
		Port:          port,
		SupabaseURL:   supabaseURL,
		SupabaseDBURL: os.Getenv("SUPABASE_DB_URL"),
	}, nil
}
