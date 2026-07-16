package docvlm

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildRequest_ValidPagesProducesVisionRequest(t *testing.T) {
	pages := []Page{
		{Index: 0, ImageB64: "Zm9v", MimeType: "image/png"},
		{Index: 1, ImageB64: "YmFy", MimeType: "image/jpeg"},
	}

	req, err := BuildRequest(pages, "extract the signature block")
	if err != nil {
		t.Fatalf("BuildRequest returned error: %v", err)
	}

	if req.Model != ModelName {
		t.Fatalf("Model = %q, want %q", req.Model, ModelName)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2 (system + user)", len(req.Messages))
	}
	if req.Messages[0].Role != "system" {
		t.Fatalf("Messages[0].Role = %q, want system", req.Messages[0].Role)
	}
	user := req.Messages[1]
	if user.Role != "user" {
		t.Fatalf("Messages[1].Role = %q, want user", user.Role)
	}
	// One text part with the caller's instructions, plus one image_url part per page.
	if len(user.Content) != len(pages)+1 {
		t.Fatalf("len(Content) = %d, want %d", len(user.Content), len(pages)+1)
	}
	if user.Content[0].Type != "text" || !strings.Contains(user.Content[0].Text, "extract the signature block") {
		t.Fatalf("Content[0] = %+v, want text part containing instructions", user.Content[0])
	}
	for i, part := range user.Content[1:] {
		if part.Type != "image_url" {
			t.Fatalf("Content[%d].Type = %q, want image_url", i+1, part.Type)
		}
		wantPrefix := "data:" + pages[i].MimeType + ";base64,"
		if !strings.HasPrefix(part.ImageURL.URL, wantPrefix) {
			t.Fatalf("Content[%d].ImageURL.URL = %q, want prefix %q", i+1, part.ImageURL.URL, wantPrefix)
		}
	}
	if req.ResponseFormat.Type != "json_object" {
		t.Fatalf("ResponseFormat.Type = %q, want json_object", req.ResponseFormat.Type)
	}

	// The request must round-trip through encoding/json (this is what the
	// agent's shell/HTTP tool actually sends on the wire).
	if _, err := json.Marshal(req); err != nil {
		t.Fatalf("json.Marshal(req): %v", err)
	}
}

func TestBuildRequest_RejectsEmptyPages(t *testing.T) {
	if _, err := BuildRequest(nil, "extract layout"); err == nil {
		t.Fatal("expected error for empty pages, got nil")
	}
}

func TestBuildRequest_RejectsPageMissingImageBytes(t *testing.T) {
	pages := []Page{{Index: 0, ImageB64: "", MimeType: "image/png"}}
	if _, err := BuildRequest(pages, "extract layout"); err == nil {
		t.Fatal("expected error for page with empty ImageB64, got nil")
	}
}

func TestBuildRequest_RejectsPageMissingMimeType(t *testing.T) {
	pages := []Page{{Index: 0, ImageB64: "Zm9v", MimeType: ""}}
	if _, err := BuildRequest(pages, "extract layout"); err == nil {
		t.Fatal("expected error for page with empty MimeType, got nil")
	}
}

func TestBuildRequest_RejectsOversizeImage(t *testing.T) {
	oversize := base64.StdEncoding.EncodeToString(make([]byte, MaxImageBytes+1))
	pages := []Page{{Index: 0, ImageB64: oversize, MimeType: "image/png"}}
	if _, err := BuildRequest(pages, "extract layout"); err == nil {
		t.Fatal("expected error for an oversize image, got nil")
	}
}

func TestBuildRequest_MimeTypeAllowlist(t *testing.T) {
	cases := []struct {
		name     string
		mimeType string
		wantErr  bool
	}{
		{"png allowed", "image/png", false},
		{"jpeg allowed", "image/jpeg", false},
		{"webp allowed", "image/webp", false},
		{"gif rejected", "image/gif", true},
		{"pdf rejected", "application/pdf", true},
		{"empty rejected", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pages := []Page{{Index: 0, ImageB64: "Zm9v", MimeType: tc.mimeType}}
			_, err := BuildRequest(pages, "extract layout")
			if tc.wantErr && err == nil {
				t.Fatalf("mime type %q: expected error, got nil", tc.mimeType)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("mime type %q: unexpected error: %v", tc.mimeType, err)
			}
		})
	}
}

func TestBuildRequest_RejectsInvalidBase64(t *testing.T) {
	pages := []Page{{Index: 0, ImageB64: "not-valid-base64!!!", MimeType: "image/png"}}
	if _, err := BuildRequest(pages, "extract layout"); err == nil {
		t.Fatal("expected error for invalid base64 image data, got nil")
	}
}

func TestBuildRequest_RejectsBlankInstructions(t *testing.T) {
	pages := []Page{{Index: 0, ImageB64: "Zm9v", MimeType: "image/png"}}
	if _, err := BuildRequest(pages, "   "); err == nil {
		t.Fatal("expected error for blank instructions, got nil")
	}
}

// TestLiteLLMConfigHasDocVLMRoute guards the deploy/litellm/config.yaml side
// of this skill: ModelName must name a real, vision-capable model_list entry
// so the request docvlm.BuildRequest produces is actually routable.
func TestLiteLLMConfigHasDocVLMRoute(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve test file path via runtime.Caller")
	}
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "..")
	configPath := filepath.Join(repoRoot, "deploy", "litellm", "config.yaml")

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read %s: %v", configPath, err)
	}

	var doc struct {
		ModelList []struct {
			ModelName     string `yaml:"model_name"`
			LiteLLMParams struct {
				Model string `yaml:"model"`
			} `yaml:"litellm_params"`
		} `yaml:"model_list"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse %s: %v", configPath, err)
	}

	for _, entry := range doc.ModelList {
		if entry.ModelName == ModelName {
			if entry.LiteLLMParams.Model == "" {
				t.Fatalf("route %s has an empty litellm_params.model", ModelName)
			}
			return
		}
	}
	t.Fatalf("deploy/litellm/config.yaml has no model_list entry named %q", ModelName)
}
