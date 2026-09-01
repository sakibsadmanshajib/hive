package ledger

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Issue #1063: chat users cannot see credits or usage left anywhere.
//
// The signed-in chat principal authenticates as an Open WebUI user whose only
// reliable identifier is an email; the balance lives on the tenant's billing
// account, and the tenant->account link in public.tenant_billing_accounts is
// service-role-only by design and must never reach a browser. So the Open
// WebUI backend resolves its own session to an email server side and calls the
// internal route below over the shared-secret gate, and this file answers with
// the smallest trimmed view the composer banner needs.
//
// Scope is deliberately TENANT balance. The schema has no per-user allowance;
// do not fake one off request_attempts.user_id.

// ChatBalanceRoute is mounted behind RequireInternalToken.
const ChatBalanceRoute = "/internal/chat/credits/balance"

// maxChatBalanceBody bounds the one-field JSON body this route accepts.
const maxChatBalanceBody = 4 << 10 // 4 KiB

type chatBalanceRequest struct {
	Email string `json:"email"`
}

// ChatBalance is what the banner renders from. Nothing else crosses: posted
// and reserved are already two more numbers than the widget needs, kept
// because they cost nothing and make the response self-describing in logs.
type ChatBalance struct {
	PostedCredits    int64 `json:"posted_credits"`
	ReservedCredits  int64 `json:"reserved_credits"`
	AvailableCredits int64 `json:"available_credits"`
	UsageToday       int64 `json:"usage_today_credits"`
}

// GetChatBalance resolves the email's tenant billing account and reads its
// balance plus today's usage. found=false means no active membership on a
// billed, non-archived tenant; that is a normal answer, not an error. The
// route above answers it with a zero balance rather than a 404 (issue #1599);
// the flag is kept because "unbilled" and "billed but empty" are genuinely
// different states to a caller that has to act on them, even though the wire
// answer is deliberately the same for both.
func (s *Service) GetChatBalance(ctx context.Context, email string) (ChatBalance, bool, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return ChatBalance{}, false, nil
	}

	accountID, err := s.repo.ResolveAccountIDForEmail(ctx, email)
	if err != nil {
		return ChatBalance{}, false, err
	}
	if accountID == uuid.Nil {
		return ChatBalance{}, false, nil
	}

	balance, err := s.GetBalance(ctx, accountID)
	if err != nil {
		return ChatBalance{}, false, err
	}

	// Midnight UTC. "Today" for a prepaid meter is a display convention, not a
	// billing boundary; the ledger itself never depends on this number.
	usageToday, err := s.repo.GetUsageSince(ctx, accountID, time.Now().UTC().Truncate(24*time.Hour))
	if err != nil {
		return ChatBalance{}, false, err
	}

	return ChatBalance{
		PostedCredits:    balance.PostedCredits,
		ReservedCredits:  balance.ReservedCredits,
		AvailableCredits: balance.AvailableCredits,
		UsageToday:       usageToday,
	}, true, nil
}

// RegisterChatBalanceRoute mounts POST /internal/chat/credits/balance behind
// the shared-secret gate every /internal route runs under.
func RegisterChatBalanceRoute(mux *http.ServeMux, svc *Service, gate func(http.Handler) http.Handler) {
	mux.Handle(ChatBalanceRoute, gate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}

		var req chatBalanceRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxChatBalanceBody))
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		if req.Email == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
			return
		}

		balance, found, err := svc.GetChatBalance(r.Context(), req.Email)
		switch {
		case err != nil:
			// Opaque on purpose: the email must not be echoed back into a
			// response body or log line attached to it.
			slog.ErrorContext(r.Context(), "chat balance read failed")
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "balance could not be read"})
		case !found:
			// Issue #1599. A principal with no billed account holds no
			// credit, and no credit is a zero balance, not a missing
			// resource. The 404 this used to answer made the frontend render
			// no banner at all (credits.ts treats every non-200 as "nothing
			// to show"), so a workspace that could not chat looked identical
			// to one that simply had nothing to display, and the only surface
			// that named the problem was edge-api refusing the next message.
			// Zero renders the "You're out of credits / Top up" state the
			// banner already carries.
			//
			// The body is the zero ChatBalance, byte-identical to a real zero
			// balance, so this route still cannot be used to probe whether a
			// given email is billed. The enterprise posture is unaffected:
			// that one is gated in the Open WebUI shim, which 404s before it
			// ever reaches this route when the control-plane URL and internal
			// token are unset.
			//
			// Accepted consequence, deliberately, not an oversight. found is
			// false for more than a seeded demo tenant: signup's
			// EnsureTenantBillingAccount records a live 28 to 60 second window
			// between the membership row and the mapping, and it declines
			// permanently for a tenant whose active members do not converge on
			// one account. Those principals now see "You're out of credits"
			// with a Top up link that will not help them, where before they
			// saw nothing at all. That is the better of the two wrong answers:
			// an empty wallet is at least a state the user can recognise and
			// escalate, silence is not, the window is seconds on the common
			// path, and the permanent case is an operator repair either way.
			// The alternative, telling the two apart on the wire, is exactly
			// the "is this email billed" oracle the identical body closes, so
			// any future split belongs on a deployment-level signal in the
			// shim, never in this response.
			writeJSON(w, http.StatusOK, ChatBalance{})
		default:
			writeJSON(w, http.StatusOK, balance)
		}
	})))
}
