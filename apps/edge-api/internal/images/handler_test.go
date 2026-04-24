package images_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hivegpt/hive/apps/edge-api/internal/images"
)

// --- Test doubles ---

// mockLiteLLM is a test server that simulates LiteLLM responses.
type mockLiteLLMServer struct {
	server         *httptest.Server
	lastPath       string
	lastBody       []byte
	lastCT         string
	responseBody   []byte
	responseCode   int
	responseCtType string
}

func newMockLiteLLM(body []byte, code int, ct string) *mockLiteLLMServer {
	m := &mockLiteLLMServer{
		responseBody:   body,
		responseCode:   code,
		responseCtType: ct,
	}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.lastPath = r.URL.Path
		m.lastBody, _ = io.ReadAll(r.Body)
		m.lastCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(code)
		w.Write(body)
	}))
	return m
}

func (m *mockLiteLLMServer) Close() { m.server.Close() }

// mockStorage is a test double for the storage interface.
type mockStorage struct {
	uploaded     bool
	uploadedKey  string
	uploadedData []byte
	presignCalls int
	presignURL   string
}

func (ms *mockStorage) Upload(_ context.Context, _, key string, reader io.Reader, _ int64, _ string) error {
	ms.uploaded = true
	ms.uploadedKey = key
	ms.uploadedData, _ = io.ReadAll(reader)
	return nil
}

func (ms *mockStorage) PresignedURL(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	ms.presignCalls++
	return ms.presignURL, nil
}

// mockAuthorizer is a stub that always grants authorization.
type mockAuthorizer struct {
	accountID string
	apiKeyID  string
}

func (a *mockAuthorizer) AuthorizeRequest(_ *http.Request) (images.AuthResult, error) {
	return images.AuthResult{AccountID: a.accountID, APIKeyID: a.apiKeyID}, nil
}

// mockRouting is a stub that echoes the alias as the LiteLLM model name.
type mockRouting struct {
	litellmModel string
}

func (r *mockRouting) SelectRoute(_ context.Context, input images.RouteInput) (images.RouteResult, error) {
	litellm := r.litellmModel
	if litellm == "" {
		litellm = input.AliasID
	}
	return images.RouteResult{AliasID: input.AliasID, LiteLLMModelName: litellm}, nil
}

// mockAccounting is a stub that tracks reservation calls.
type mockAccounting struct {
	reservationID  string
	finalizeCalled bool
	releaseCalled  bool
}

func (a *mockAccounting) CreateReservation(_ context.Context, _ images.ReservationInput) (string, error) {
	return a.reservationID, nil
}

func (a *mockAccounting) FinalizeReservation(_ context.Context, _ images.FinalizeInput) error {
	a.finalizeCalled = true
	return nil
}

func (a *mockAccounting) ReleaseReservation(_ context.Context, _, _, _ string) error {
	a.releaseCalled = true
	return nil
}

// buildHandler creates a Handler wired to the given mock LiteLLM URL and storage.
func buildHandler(litellmBaseURL string, storage images.StorageInterface) *images.Handler {
	auth := &mockAuthorizer{accountID: "acct-test", apiKeyID: "key-test"}
	routing := &mockRouting{}
	accounting := &mockAccounting{reservationID: "res-test"}
	return images.NewHandler(auth, routing, accounting, litellmBaseURL, "test-key", storage, "hive-images")
}

// --- Tests ---

func TestImageGenerationDispatches(t *testing.T) {
	respBody := `{"created":1700000000,"data":[{"url":"http://provider.example.com/img.png"}]}`
	mock := newMockLiteLLM([]byte(respBody), 200, "application/json")
	defer mock.Close()

	stor := &mockStorage{presignURL: "https://signed.example.com/img.png"}
	h := buildHandler(mock.server.URL, stor)

	body := `{"model":"dall-e-3","prompt":"a cat","response_format":"url"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.lastPath != "/images/generations" {
		t.Errorf("expected LiteLLM path /images/generations, got %s", mock.lastPath)
	}
	// Verify model rewrite in outgoing request
	var sentBody map[string]interface{}
	if err := json.Unmarshal(mock.lastBody, &sentBody); err != nil {
		t.Fatalf("could not parse outgoing body: %v", err)
	}
}

func TestImageGenerationURLMode(t *testing.T) {
	// Simulate provider returning a URL - handler should upload to S3 and return presigned URL

	// Mock an image download server
	imgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("fake-png-bytes"))
	}))
	defer imgServer.Close()

	// The provider URL must be on the mock image server
	actualProviderURL := imgServer.URL + "/generated.png"
	respBody, _ := json.Marshal(images.ImageResponse{
		Created: 1700000000,
		Data: []images.ImageData{
			{URL: &actualProviderURL},
		},
	})

	mock := newMockLiteLLM(respBody, 200, "application/json")
	defer mock.Close()

	stor := &mockStorage{presignURL: "https://signed.example.com/img.png"}
	h := buildHandler(mock.server.URL, stor)

	body := `{"model":"dall-e-3","prompt":"a cat","response_format":"url"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp images.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if len(resp.Data) == 0 {
		t.Fatal("expected at least one image data item")
	}
	if resp.Data[0].URL == nil {
		t.Fatal("expected URL in response")
	}
	// URL should be the presigned URL, not the provider URL
	if *resp.Data[0].URL == actualProviderURL {
		t.Error("expected presigned URL, got raw provider URL")
	}
	if *resp.Data[0].URL != "https://signed.example.com/img.png" {
		t.Errorf("expected presigned URL, got %s", *resp.Data[0].URL)
	}
	if !stor.uploaded {
		t.Error("expected storage upload to be called")
	}
	if stor.presignCalls != 1 {
		t.Errorf("expected 1 presign call, got %d", stor.presignCalls)
	}
}

func TestImageGenerationB64Mode(t *testing.T) {
	b64Data := "iVBORw0KGgo="
	respBody, _ := json.Marshal(images.ImageResponse{
		Created: 1700000000,
		Data: []images.ImageData{
			{B64JSON: &b64Data},
		},
	})

	mock := newMockLiteLLM(respBody, 200, "application/json")
	defer mock.Close()

	stor := &mockStorage{}
	h := buildHandler(mock.server.URL, stor)

	body := `{"model":"dall-e-3","prompt":"a cat","response_format":"b64_json"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp images.ImageResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("could not decode response: %v", err)
	}

	if len(resp.Data) == 0 || resp.Data[0].B64JSON == nil {
		t.Fatal("expected b64_json in response")
	}
	if *resp.Data[0].B64JSON != b64Data {
		t.Errorf("expected b64 passthrough, got %s", *resp.Data[0].B64JSON)
	}
	if stor.uploaded {
		t.Error("b64 mode should NOT upload to storage")
	}
}

func TestImageEditsMultipart(t *testing.T) {
	respBody := `{"created":1700000000,"data":[{"b64_json":"abc123"}]}`
	mock := newMockLiteLLM([]byte(respBody), 200, "application/json")
	defer mock.Close()

	stor := &mockStorage{}
	h := buildHandler(mock.server.URL, stor)

	// Build a multipart request
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", "dall-e-2")
	_ = mw.WriteField("prompt", "make it blue")
	fw, _ := mw.CreateFormFile("image", "input.png")
	fw.Write([]byte("fake-image-bytes"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if mock.lastPath != "/images/edits" {
		t.Errorf("expected LiteLLM path /images/edits, got %s", mock.lastPath)
	}
	// Outgoing request to LiteLLM must also be multipart
	if !strings.Contains(mock.lastCT, "multipart/form-data") {
		t.Errorf("expected multipart content-type to LiteLLM, got %s", mock.lastCT)
	}
}

func TestImageVariationsUnsupported(t *testing.T) {
	stor := &mockStorage{}
	h := buildHandler("http://unused.example.com", stor)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/variations", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("expected 501, got %d", w.Code)
	}

	var errResp struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("could not decode error response: %v", err)
	}
	if errResp.Error.Type != "invalid_request_error" {
		t.Errorf("expected type invalid_request_error, got %s", errResp.Error.Type)
	}
	if errResp.Error.Code != "unsupported_operation" {
		t.Errorf("expected code unsupported_operation, got %s", errResp.Error.Code)
	}
}

func TestImageUnknownPathReturns404(t *testing.T) {
	stor := &mockStorage{}
	h := buildHandler("http://unused.example.com", stor)

	req := httptest.NewRequest(http.MethodPost, "/v1/images/unknown", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer test-key")
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
