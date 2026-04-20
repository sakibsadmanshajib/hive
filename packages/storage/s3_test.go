package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewS3ClientRequiresConfig(t *testing.T) {
	base := testConfig("https://project.supabase.co/storage/v1/s3")
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "endpoint",
			mutate: func(cfg *Config) {
				cfg.Endpoint = ""
			},
			wantErr: "S3_ENDPOINT is required",
		},
		{
			name: "access key",
			mutate: func(cfg *Config) {
				cfg.AccessKey = ""
			},
			wantErr: "S3_ACCESS_KEY is required",
		},
		{
			name: "secret key",
			mutate: func(cfg *Config) {
				cfg.SecretKey = ""
			},
			wantErr: "S3_SECRET_KEY is required",
		},
		{
			name: "region",
			mutate: func(cfg *Config) {
				cfg.Region = ""
			},
			wantErr: "S3_REGION is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := base
			tt.mutate(&cfg)
			_, err := NewS3Client(cfg)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("NewS3Client error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestS3ClientUploadUsesEndpointPath(t *testing.T) {
	ctx := context.Background()
	const wantPath = "/storage/v1/s3/hive-files/acct/file.jsonl"
	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPut {
			t.Errorf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", auth)
		}
		if payloadHash := r.Header.Get("x-amz-content-sha256"); payloadHash == "" {
			t.Error("x-amz-content-sha256 header is empty")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if string(body) != `{"ok":true}` {
			t.Errorf("body = %q, want JSON fixture", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL+"/storage/v1/s3")
	err := client.Upload(ctx, "hive-files", "acct/file.jsonl", strings.NewReader(`{"ok":true}`), int64(len(`{"ok":true}`)), "application/jsonl")
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
	if !sawRequest {
		t.Fatal("Upload did not send request to test server")
	}
}

func TestS3ClientUploadEscapesBucketAndKeySegments(t *testing.T) {
	ctx := context.Background()
	const wantRequestURI = "/storage/v1/s3/hive%20files/acct%20one/file%20%231.jsonl"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.EscapedPath(); got != wantRequestURI {
			t.Errorf("escaped path = %q, want %q", got, wantRequestURI)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL+"/storage/v1/s3/")
	err := client.Upload(ctx, "hive files", "acct one/file #1.jsonl", strings.NewReader("body"), int64(len("body")), "text/plain")
	if err != nil {
		t.Fatalf("Upload returned error: %v", err)
	}
}

func TestS3ClientDownloadAndDeleteUseEndpointPath(t *testing.T) {
	ctx := context.Background()
	const wantPath = "/storage/v1/s3/hive-files/acct/file.jsonl"
	requests := make([]string, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", auth)
		}
		if payloadHash := r.Header.Get("x-amz-content-sha256"); payloadHash == "" {
			t.Error("x-amz-content-sha256 header is empty")
		}
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write([]byte("downloaded"))
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL+"/storage/v1/s3")
	body, err := client.Download(ctx, "hive-files", "acct/file.jsonl")
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	defer body.Close()
	gotBody, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read downloaded body: %v", err)
	}
	if string(gotBody) != "downloaded" {
		t.Fatalf("download body = %q, want downloaded", gotBody)
	}
	if err := client.Delete(ctx, "hive-files", "acct/file.jsonl"); err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	want := []string{"GET " + wantPath, "DELETE " + wantPath}
	if strings.Join(requests, ",") != strings.Join(want, ",") {
		t.Fatalf("requests = %v, want %v", requests, want)
	}
}

func TestS3ClientPresignedURLUsesSigV4Query(t *testing.T) {
	client := newTestClient(t, "https://project.supabase.co/storage/v1/s3")
	rawURL, err := client.PresignedURL(context.Background(), "hive-files", "acct/file.jsonl", time.Hour)
	if err != nil {
		t.Fatalf("PresignedURL returned error: %v", err)
	}
	for _, fragment := range []string{
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=",
		"X-Amz-Signature=",
		"X-Amz-Expires=3600",
	} {
		if !strings.Contains(rawURL, fragment) {
			t.Fatalf("presigned URL %q missing %q", rawURL, fragment)
		}
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse presigned URL: %v", err)
	}
	if parsed.Path != "/storage/v1/s3/hive-files/acct/file.jsonl" {
		t.Fatalf("path = %q, want Supabase S3 path-style object path", parsed.Path)
	}
	query := parsed.Query()
	assertQueryValue(t, query, "X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	if query.Get("X-Amz-Credential") == "" {
		t.Fatal("X-Amz-Credential is empty")
	}
	if query.Get("X-Amz-Signature") == "" {
		t.Fatal("X-Amz-Signature is empty")
	}
	assertQueryValue(t, query, "X-Amz-Expires", "3600")
}

func TestS3ClientMultipartRequestsUseS3Queries(t *testing.T) {
	ctx := context.Background()
	const wantPath = "/storage/v1/s3/hive-files/acct/file.jsonl"
	var steps []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		steps = append(steps, r.Method+"?"+r.URL.RawQuery)
		if r.URL.Path != wantPath {
			t.Errorf("path = %q, want %q", r.URL.Path, wantPath)
		}
		if auth := r.Header.Get("Authorization"); !strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
			t.Errorf("Authorization = %q, want AWS4-HMAC-SHA256 prefix", auth)
		}
		if payloadHash := r.Header.Get("x-amz-content-sha256"); payloadHash == "" {
			t.Error("x-amz-content-sha256 header is empty")
		}

		switch {
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploads=":
			if contentType := r.Header.Get("Content-Type"); contentType != "application/jsonl" {
				t.Errorf("Content-Type = %q, want application/jsonl", contentType)
			}
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>upload-123</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPut && r.URL.RawQuery == "partNumber=1&uploadId=upload-123":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read part body: %v", err)
			}
			if !bytes.Equal(body, []byte("part-one")) {
				t.Errorf("part body = %q, want part-one", body)
			}
			w.Header().Set("ETag", `"etag-1"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && r.URL.RawQuery == "uploadId=upload-123":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read complete body: %v", err)
			}
			if !strings.Contains(string(body), "CompleteMultipartUpload") {
				t.Errorf("complete body = %q, want CompleteMultipartUpload XML", body)
			}
			if !strings.Contains(string(body), "<PartNumber>1</PartNumber>") || !strings.Contains(string(body), "<ETag>etag-1</ETag>") {
				t.Errorf("complete body missing part fields: %q", body)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.RawQuery == "uploadId=upload-123":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected multipart request %s ?%s", r.Method, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)

	client := newTestClient(t, server.URL+"/storage/v1/s3")
	uploadID, err := client.InitMultipartUpload(ctx, "hive-files", "acct/file.jsonl", "application/jsonl")
	if err != nil {
		t.Fatalf("InitMultipartUpload returned error: %v", err)
	}
	if uploadID != "upload-123" {
		t.Fatalf("uploadID = %q, want upload-123", uploadID)
	}
	etag, err := client.UploadPart(ctx, "hive-files", "acct/file.jsonl", uploadID, 1, strings.NewReader("part-one"), int64(len("part-one")))
	if err != nil {
		t.Fatalf("UploadPart returned error: %v", err)
	}
	if etag != "etag-1" {
		t.Fatalf("etag = %q, want etag-1", etag)
	}
	if err := client.CompleteMultipartUpload(ctx, "hive-files", "acct/file.jsonl", uploadID, []CompletePart{{PartNumber: 1, ETag: etag}}); err != nil {
		t.Fatalf("CompleteMultipartUpload returned error: %v", err)
	}
	if err := client.AbortMultipartUpload(ctx, "hive-files", "acct/file.jsonl", uploadID); err != nil {
		t.Fatalf("AbortMultipartUpload returned error: %v", err)
	}
	want := []string{"POST?uploads=", "PUT?partNumber=1&uploadId=upload-123", "POST?uploadId=upload-123", "DELETE?uploadId=upload-123"}
	if strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("multipart requests = %v, want %v", steps, want)
	}
}

func newTestClient(t *testing.T, endpoint string) *S3Client {
	t.Helper()
	client, err := NewS3Client(testConfig(endpoint))
	if err != nil {
		t.Fatalf("NewS3Client returned error: %v", err)
	}
	return client
}

func testConfig(endpoint string) Config {
	return Config{
		Endpoint:  endpoint,
		AccessKey: "test-access",
		SecretKey: "test-secret",
		Region:    "us-east-1",
		Now: func() time.Time {
			return time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC)
		},
	}
}

func assertQueryValue(t *testing.T, query url.Values, key, want string) {
	t.Helper()
	if got := query.Get(key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
