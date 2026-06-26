package sovereign_test

import (
	"strings"
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/sovereign"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name        string
		env         map[string]string
		wantErr     bool
		errContains string // non-empty: assert substring present in error
	}{
		// --- flag disabled ---
		{
			name:    "sovereign off (false), external keys set — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "false", "OPENROUTER_API_KEY": "sk-test"},
			wantErr: false,
		},
		{
			name:    "sovereign off (0), external keys set — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "0", "OPENROUTER_API_KEY": "sk-test"},
			wantErr: false,
		},
		{
			name:    "sovereign off (no), external keys set — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "no", "OPENROUTER_API_KEY": "sk-test"},
			wantErr: false,
		},
		{
			name:    "sovereign unset, external keys set — boots (unchanged behaviour)",
			env:     map[string]string{"OPENROUTER_API_KEY": "sk-test", "GROQ_API_KEY": "gsk-test"},
			wantErr: false,
		},
		// --- flag enabled: truthy variants ---
		{
			name:    "sovereign on (true lowercase), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "true"},
			wantErr: false,
		},
		{
			name:    "sovereign on (TRUE uppercase), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "TRUE"},
			wantErr: false,
		},
		{
			name:    "sovereign on (True mixed), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "True"},
			wantErr: false,
		},
		{
			name:    "sovereign on (1), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "1"},
			wantErr: false,
		},
		{
			name:    "sovereign on (yes), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "yes"},
			wantErr: false,
		},
		{
			name:    "sovereign on (on), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "on"},
			wantErr: false,
		},
		{
			name:    "sovereign on (YES uppercase), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "YES"},
			wantErr: false,
		},
		{
			name:    "sovereign on (ON uppercase), no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "ON"},
			wantErr: false,
		},
		// --- flag enabled: violation cases ---
		{
			name:        "sovereign on, OPENROUTER_API_KEY set — startup error",
			env:         map[string]string{"HIVE_SOVEREIGN": "true", "OPENROUTER_API_KEY": "sk-openrouter"},
			wantErr:     true,
			errContains: "OPENROUTER_API_KEY",
		},
		{
			name:        "sovereign on (1), GROQ_API_KEY set — startup error",
			env:         map[string]string{"HIVE_SOVEREIGN": "1", "GROQ_API_KEY": "gsk-groq"},
			wantErr:     true,
			errContains: "GROQ_API_KEY",
		},
		{
			name:        "sovereign on (TRUE), both external keys set — reports both in one error",
			env:         map[string]string{"HIVE_SOVEREIGN": "TRUE", "OPENROUTER_API_KEY": "sk-openrouter", "GROQ_API_KEY": "gsk-groq"},
			wantErr:     true,
			errContains: "OPENROUTER_API_KEY",
		},
		{
			name:        "sovereign on (yes), both keys — error also mentions GROQ_API_KEY",
			env:         map[string]string{"HIVE_SOVEREIGN": "yes", "OPENROUTER_API_KEY": "sk-openrouter", "GROQ_API_KEY": "gsk-groq"},
			wantErr:     true,
			errContains: "GROQ_API_KEY",
		},
		// --- edge: whitespace / empty ---
		{
			name:    "sovereign on, whitespace-only key — not treated as set",
			env:     map[string]string{"HIVE_SOVEREIGN": "true", "OPENROUTER_API_KEY": "   "},
			wantErr: false,
		},
		{
			name:    "sovereign on, empty key — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "true", "OPENROUTER_API_KEY": ""},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lookup := func(key string) string { return tc.env[key] }
			err := sovereign.Check(lookup)
			if (err != nil) != tc.wantErr {
				t.Errorf("Check() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.errContains != "" && err != nil && !strings.Contains(err.Error(), tc.errContains) {
				t.Errorf("Check() error = %q, want substring %q", err.Error(), tc.errContains)
			}
		})
	}
}
