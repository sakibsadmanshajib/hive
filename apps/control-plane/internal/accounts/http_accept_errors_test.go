package accounts

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The case order in writeAcceptInvitationError is load-bearing. A failed
// membership activation wraps its cause, and that cause is usually ErrNotFound
// (zero rows), so an ErrNotFound case matched first would answer 404 "this
// invitation link is not valid" to somebody whose invitation was fine. These
// cases pin the order and the wording, since both are what the invitee sees.
func TestWriteAcceptInvitationError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "activation failure is a server fault, not a bad link",
			err:        fmt.Errorf("%w: %w", ErrMembershipActivation, ErrNotFound),
			wantStatus: http.StatusInternalServerError,
			wantBody:   "could not accept the invitation",
		},
		{
			name:       "an unknown token really is a bad link",
			err:        ErrNotFound,
			wantStatus: http.StatusNotFound,
			wantBody:   "this invitation link is not valid",
		},
		{
			name:       "an unverified viewer is told what to do about it",
			err:        ErrEmailNotVerified,
			wantStatus: http.StatusForbidden,
			wantBody:   "verify your email address",
		},
		{
			name:       "the loser of a concurrent first acceptance is a member, not a failure",
			err:        ErrAlreadyMember,
			wantStatus: http.StatusConflict,
			wantBody:   "already a member",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeAcceptInvitationError(rr, httptest.NewRequest(http.MethodPost, "/api/v1/invitations/accept", nil), tc.err)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if !strings.Contains(rr.Body.String(), tc.wantBody) {
				t.Fatalf("body %s does not contain %q", rr.Body.String(), tc.wantBody)
			}
			// Whatever the case, the raw error text never reaches the client.
			if strings.Contains(rr.Body.String(), "accounts:") {
				t.Fatalf("internal error text leaked to the client: %s", rr.Body.String())
			}
		})
	}
}
