package sovereign_test

import (
	"testing"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/sovereign"
)

func TestCheck(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
	}{
		{
			name:    "sovereign off, no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "false"},
			wantErr: false,
		},
		{
			name:    "sovereign unset, external keys set — boots (unchanged behaviour)",
			env:     map[string]string{"OPENROUTER_API_KEY": "sk-test", "GROQ_API_KEY": "gsk-test"},
			wantErr: false,
		},
		{
			name:    "sovereign on, no external keys — boots",
			env:     map[string]string{"HIVE_SOVEREIGN": "true"},
			wantErr: false,
		},
		{
			name:    "sovereign on, OPENROUTER_API_KEY set — startup error",
			env:     map[string]string{"HIVE_SOVEREIGN": "true", "OPENROUTER_API_KEY": "sk-openrouter"},
			wantErr: true,
		},
		{
			name:    "sovereign on, GROQ_API_KEY set — startup error",
			env:     map[string]string{"HIVE_SOVEREIGN": "true", "GROQ_API_KEY": "gsk-groq"},
			wantErr: true,
		},
		{
			name:    "sovereign on, both external keys set — startup error",
			env:     map[string]string{"HIVE_SOVEREIGN": "true", "OPENROUTER_API_KEY": "sk-openrouter", "GROQ_API_KEY": "gsk-groq"},
			wantErr: true,
		},
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
		})
	}
}
