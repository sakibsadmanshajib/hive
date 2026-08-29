package storage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
)

// A signed request must go on the wire in origin form.
//
// SignHTTP sets URL.Opaque to "//host/path" so the AWS signer sees an
// un-normalized path. net/url.RequestURI returns Opaque verbatim and glues the
// scheme back on when it starts with "//", so leaving it set writes
// `PUT http://host/s3/bucket/key HTTP/1.1`. RFC 9112 reserves that absolute
// form for requests to a proxy. An origin server expects `PUT /s3/bucket/key`.
//
// This is not academic. Supabase Storage builds its canonical URI as
// `new URL("http://localhost:8080" + prefix + request.url)`, so an absolute
// target yields `http://localhost:8080http://host/...` and every upload fails
// with a 500 ERR_INVALID_URL naming neither the path nor the credential. The
// deployed box never saw it because Caddy normalizes the target before
// proxying, so this only surfaced once CI got a real object store to talk to
// (issue #1324).
func TestSignedRequestUsesOriginForm(t *testing.T) {
	req, err := http.NewRequest(http.MethodPut,
		"http://storage.example:5000/s3/hive-files/tenant/file-1/input.jsonl", strings.NewReader("x"))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.ContentLength = 1

	signer := v4.NewSigner()
	if err := SignHTTP(context.Background(), signer, req, "access", "secret", "us-east-1",
		time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC), ""); err != nil {
		t.Fatalf("sign: %v", err)
	}

	if got := req.URL.RequestURI(); got != "/s3/hive-files/tenant/file-1/input.jsonl" {
		t.Fatalf("request target = %q, want the origin form /s3/hive-files/tenant/file-1/input.jsonl.\n"+
			"An absolute target is only legal to a proxy, and Supabase Storage answers it with a 500 "+
			"ERR_INVALID_URL rather than a signature error, so this fails in a way that names nothing.", got)
	}
	if req.Header.Get("Authorization") == "" {
		t.Fatal("no Authorization header, so this test would pass on an unsigned request")
	}
}

// The same property, asserted on the bytes an origin server actually reads,
// because RequestURI() is the value net/http writes but not proof that it did.
func TestUploadWritesOriginFormRequestLine(t *testing.T) {
	var requestLine string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestLine = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := NewS3Client(Config{
		Endpoint:  server.URL + "/s3",
		AccessKey: "access",
		SecretKey: "secret",
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	if err := client.Upload(context.Background(), "hive-files", "tenant/file-1/input.jsonl",
		strings.NewReader("x"), 1, "application/jsonl"); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if !strings.HasPrefix(requestLine, "/") {
		t.Fatalf("server read request target %q, want an origin-form path starting with /", requestLine)
	}
	if want := "/s3/hive-files/tenant/file-1/input.jsonl"; requestLine != want {
		t.Fatalf("server read request target %q, want %q", requestLine, want)
	}
}

// Guard for the half that must NOT change. presignHTTP relies on Opaque
// surviving, because url.URL.String renders a bare-path Opaque as
// `http:/s3/...`; the "//host" form is what makes the presigned URL absolute.
// A future cleanup that clears Opaque in both places would produce presigned
// URLs no client can resolve.
func TestPresignStillProducesAnAbsoluteURL(t *testing.T) {
	client, err := NewS3Client(Config{
		Endpoint:  "http://storage.example:5000/s3",
		AccessKey: "access",
		SecretKey: "secret",
		Region:    "us-east-1",
	})
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	signed, err := client.PresignedURL(context.Background(), "hive-files", "tenant/file-1/input.jsonl", time.Minute)
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if !strings.HasPrefix(signed, "http://storage.example:5000/") {
		t.Fatalf("presigned URL = %q, want an absolute http URL on the endpoint host", signed)
	}
}

