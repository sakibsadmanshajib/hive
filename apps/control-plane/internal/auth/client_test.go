package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLookupUserFallsBackToSupabaseEmailConfirmation(t *testing.T) {
	confirmedAt := "2026-04-22T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth/v1/user" {
			t.Fatalf("expected /auth/v1/user, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"11111111-1111-1111-1111-111111111111",
			"email":"verified@example.com",
			"email_confirmed_at":"` + confirmedAt + `",
			"user_metadata":{"full_name":"Verified User"}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "anon-key")
	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if !viewer.EmailVerified {
		t.Fatal("expected email_verified=true when email_confirmed_at is present")
	}
}

func TestLookupUserAppMetadataOverrideCanForceUnverified(t *testing.T) {
	confirmedAt := "2026-04-22T00:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"22222222-2222-2222-2222-222222222222",
			"email":"gated@example.com",
			"email_confirmed_at":"` + confirmedAt + `",
			"app_metadata":{"hive_email_verified":false},
			"user_metadata":{"full_name":"Gated User"}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "anon-key")
	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if viewer.EmailVerified {
		t.Fatal("expected app_metadata override to force email_verified=false")
	}
}

func TestLookupUserAppMetadataOverrideCanForceVerified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"33333333-3333-3333-3333-333333333333",
			"email":"verified-by-hive@example.com",
			"email_confirmed_at":null,
			"app_metadata":{"hive_email_verified":true},
			"user_metadata":{"full_name":"Hive Verified"}
		}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "anon-key")
	viewer, err := client.LookupUser(context.Background(), "bearer-token")
	if err != nil {
		t.Fatalf("LookupUser error: %v", err)
	}

	if !viewer.EmailVerified {
		t.Fatal("expected app_metadata override to force email_verified=true")
	}
}
