package identity_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/identity"
)

func newReq(ctx context.Context) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/accounts/current/email-verification/finalize", nil).WithContext(ctx)
}

func TestFinalize_RejectsMissingViewer(t *testing.T) {
	h := identity.NewHandler(identity.Deps{
		FinalizeEmailVerified: func(context.Context, uuid.UUID) (int64, error) { return 1, nil },
	})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq(context.Background()))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without an authenticated viewer, got %d", rr.Code)
	}
}

func TestFinalize_FailsLoudWhenUnconfigured(t *testing.T) {
	// No FinalizeEmailVerified wired → the service-role-backed write is absent.
	// This MUST fail loud (500), never silently report success (the #112 bug).
	h := identity.NewHandler(identity.Deps{})
	ctx := auth.WithViewer(context.Background(), auth.Viewer{UserID: uuid.New()})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq(ctx))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 (fail loud) when finalize dependency is unset, got %d", rr.Code)
	}
}

func TestFinalize_PropagatesWriteErrorAsLoud500(t *testing.T) {
	h := identity.NewHandler(identity.Deps{
		FinalizeEmailVerified: func(context.Context, uuid.UUID) (int64, error) {
			return 0, errors.New("boom: connection refused")
		},
	})
	ctx := auth.WithViewer(context.Background(), auth.Viewer{UserID: uuid.New()})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq(ctx))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 on write error, got %d", rr.Code)
	}
	// The raw upstream/DB error text must never reach the client body.
	if body := rr.Body.String(); contains(body, "connection refused") || contains(body, "boom") {
		t.Fatalf("internal error text leaked to client: %q", body)
	}
}

func TestFinalize_NotConfirmedYieldsConflict(t *testing.T) {
	// 0 rows affected → Supabase has not confirmed the email (email_confirmed_at
	// IS NULL guard). We must NOT report success; surface 409 instead.
	var gotUser uuid.UUID
	want := uuid.New()
	h := identity.NewHandler(identity.Deps{
		FinalizeEmailVerified: func(_ context.Context, id uuid.UUID) (int64, error) {
			gotUser = id
			return 0, nil
		},
	})
	ctx := auth.WithViewer(context.Background(), auth.Viewer{UserID: want})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq(ctx))
	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409 when email not confirmed, got %d", rr.Code)
	}
	if gotUser != want {
		t.Fatalf("handler must finalize the authenticated viewer's id; got %s want %s", gotUser, want)
	}
}

func TestFinalize_SuccessReturnsOK(t *testing.T) {
	h := identity.NewHandler(identity.Deps{
		FinalizeEmailVerified: func(context.Context, uuid.UUID) (int64, error) { return 1, nil },
	})
	ctx := auth.WithViewer(context.Background(), auth.Viewer{UserID: uuid.New()})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, newReq(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 on successful finalize, got %d", rr.Code)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
