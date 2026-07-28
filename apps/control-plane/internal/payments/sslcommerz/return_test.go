package sslcommerz_test

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/payments"
)

// TestSSLCommerzInitiate_BrowserReturnsGoToTheConsole guards issue #538: the
// three browser-facing URLs (success, fail, cancel) must land on the console
// origin, never on a control-plane webhook. The IPN URL is the only one that
// stays on the control-plane, because it is the server-to-server settlement
// trigger.
func TestSSLCommerzInitiate_BrowserReturnsGoToTheConsole(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultSSLInput()
	if _, err := rail.Initiate(ctx, input); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	if len(ss.initiateBodies) != 1 {
		t.Fatalf("expected 1 initiate request, got %d", len(ss.initiateBodies))
	}
	form := ss.initiateBodies[0]

	for _, field := range []string{"success_url", "fail_url", "cancel_url"} {
		raw := form.Get(field)
		if raw == "" {
			t.Fatalf("%s was not sent", field)
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("%s does not parse: %v", field, err)
		}
		if parsed.Host != "console.example.com" {
			t.Errorf("%s must target the console origin, got %q", field, raw)
		}
		if strings.Contains(parsed.Path, "/webhooks/") {
			t.Errorf("%s must not target a webhook, got %q", field, raw)
		}
		if parsed.Path != payments.SSLCommerzReturnPath {
			t.Errorf("%s expected path %q, got %q", field, payments.SSLCommerzReturnPath, parsed.Path)
		}
		if parsed.Query().Get("intent") != input.PaymentIntentID.String() {
			t.Errorf("%s must carry the intent id, got %q", field, raw)
		}
	}

	if got := form.Get("cancel_url"); !strings.Contains(got, "hint="+payments.ReturnHintCancelled) {
		t.Errorf("cancel_url should carry the cancelled copy hint, got %q", got)
	}
	if got := form.Get("success_url"); strings.Contains(got, "hint=") {
		t.Errorf("success_url must not carry any outcome hint, got %q", got)
	}
}

func TestSSLCommerzInitiate_IPNStaysOnTheControlPlane(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := rail.Initiate(ctx, defaultSSLInput()); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	ipn := ss.initiateBodies[0].Get("ipn_url")
	if ipn != "https://cp.example.com/webhooks/sslcommerz/ipn" {
		t.Errorf("expected the control-plane IPN webhook, got %q", ipn)
	}
}

func TestSSLCommerzInitiate_RequiresAReturnOrigin(t *testing.T) {
	ss := newSSLServer(t)
	rail := ss.newRail()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	input := defaultSSLInput()
	input.ReturnBaseURL = ""

	if _, err := rail.Initiate(ctx, input); err == nil {
		t.Fatal("expected Initiate to refuse a checkout with no console return origin")
	}
	if len(ss.initiateBodies) != 0 {
		t.Error("the provider must not be called when the return origin is missing")
	}
}
