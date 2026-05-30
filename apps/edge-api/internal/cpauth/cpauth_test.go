package cpauth

import (
	"net/http"
	"testing"
)

func TestSetHeaderAttachesTokenWhenConfigured(t *testing.T) {
	orig := token
	defer func() { token = orig }()

	SetTokenForTest("s3cret")
	req, _ := http.NewRequest(http.MethodPost, "http://control-plane/internal/apikeys/resolve", nil)
	SetHeader(req)
	if got := req.Header.Get(Header); got != "s3cret" {
		t.Fatalf("expected %s header to be set to the token, got %q", Header, got)
	}
}

func TestSetHeaderNoopWhenUnconfigured(t *testing.T) {
	orig := token
	defer func() { token = orig }()

	SetTokenForTest("")
	req, _ := http.NewRequest(http.MethodPost, "http://control-plane/internal/apikeys/resolve", nil)
	SetHeader(req)
	if _, ok := req.Header[Header]; ok {
		t.Fatalf("expected no %s header when token unconfigured", Header)
	}
}
