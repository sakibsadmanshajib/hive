package accounts

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
)

// Handler handles all accounts-related HTTP routes.
type Handler struct {
	svc     *Service
	roleSvc *platform.RoleService // optional — used by handleListMembers to populate Actor.IsAdmin
	policy  authz.Policy
}

// NewHandler returns a new accounts Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, policy: authz.NewPolicy()}
}

// WithRoleService returns a copy of the handler whose underlying Service is
// wired with the platform role service, so GET /api/v1/viewer reports the
// real platform-admin overlay in permissions[], and handleListMembers can
// resolve the same overlay for its own independently-built Actor. Mirrors the
// apikeys/budgets handler idiom.
func (h *Handler) WithRoleService(roleSvc *platform.RoleService) *Handler {
	cloned := *h
	cloned.svc = h.svc.WithRoleService(roleSvc)
	cloned.roleSvc = roleSvc
	return &cloned
}

// WithTenantRoleService returns a copy of the handler whose underlying Service
// is wired with the tenant role service, so GET /api/v1/viewer can report
// workspace_admin from public.tenant_users: the table
// platform.WorkspaceAdminGate gates the workspace-administration surfaces on,
// as opposed to the account_memberships role the response already carries
// (issue #1660).
func (h *Handler) WithTenantRoleService(tenantRoleSvc *platform.TenantRoleService) *Handler {
	cloned := *h
	cloned.svc = h.svc.WithTenantRoleService(tenantRoleSvc)
	return &cloned
}

// ServeHTTP dispatches requests to the appropriate sub-handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/viewer":
		h.handleGetViewer(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/accounts/current/members":
		h.handleListMembers(w, r)
	case r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, memberRolePathPrefix):
		h.handleUpdateMemberRole(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/invitations":
		h.handleCreateInvitation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/accounts/current/invitations/revoke":
		h.handleRevokeInvitation(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/invitations/accept":
		h.handleAcceptInvitation(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// handleGetViewer implements GET /api/v1/viewer
func (h *Handler) handleGetViewer(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeInternal(w, r, "could not load your workspace", err)
		return
	}

	writeJSON(w, http.StatusOK, viewerContextResponse(vc))
}

// handleListMembers implements GET /api/v1/accounts/current/members
func (h *Handler) handleListMembers(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeInternal(w, r, "could not load your workspace", err)
		return
	}

	// Phase 18: route authz through policy.Can — replaces bare EmailVerified check.
	// isAdmin resolves the real platform-admin overlay when roleSvc is wired
	// (see WithRoleService), so a real platform admin who is not a workspace
	// owner is granted members.invite here too, matching the viewer
	// permissions[] overlay fixed in EnsureViewerContext.
	isAdmin := false
	if h.roleSvc != nil {
		admin, err := h.roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			slog.ErrorContext(r.Context(), "accounts: platform-admin lookup failed",
				slog.String("user_id", viewer.UserID.String()),
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authorization unavailable"})
			return
		}
		isAdmin = admin
	}
	actor := ActorFor(viewer, Membership{
		AccountID: vc.CurrentAccount.ID,
		UserID:    viewer.UserID,
		Role:      vc.CurrentAccount.Role,
		Status:    StatusActive,
	}, isAdmin)
	if !h.policy.Can(actor, authz.PermMembersInvite) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "email must be verified before accessing members",
			"code":  "email_verification_required",
		})
		return
	}

	members, err := h.svc.ListMembers(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeInternal(w, r, "could not list workspace members", err)
		return
	}

	type memberItem struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	items := make([]memberItem, 0, len(members))
	for _, m := range members {
		items = append(items, memberItem{
			UserID: m.UserID.String(),
			Email:  m.Email,
			Role:   m.Role,
			Status: m.Status,
		})
	}

	// Outstanding invitations ride along on the members payload rather than on a
	// second endpoint. They are the same question ("who is in this workspace, and
	// who is on the way in") behind the same permission, and an invited address
	// appearing nowhere is half of why an undeliverable invitation went unnoticed
	// for so long (issue #1440).
	invitations, err := h.svc.ListInvitations(r.Context(), vc.CurrentAccount.ID, viewer)
	if err != nil {
		writeInternal(w, r, "could not list workspace invitations", err)
		return
	}
	type invitationItem struct {
		ID        string `json:"id"`
		Email     string `json:"email"`
		Role      string `json:"role"`
		Status    string `json:"status"`
		ExpiresAt string `json:"expires_at"`
		CreatedAt string `json:"created_at"`
	}
	invitationItems := make([]invitationItem, 0, len(invitations))
	for _, inv := range invitations {
		invitationItems = append(invitationItems, invitationItem{
			ID:        inv.ID.String(),
			Email:     inv.Email,
			Role:      inv.Role,
			Status:    inv.Status,
			ExpiresAt: inv.ExpiresAt.UTC().Format(time.RFC3339),
			CreatedAt: inv.CreatedAt.UTC().Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"members":     items,
		"invitations": invitationItems,
	})
}

// handleRevokeInvitation implements POST /api/v1/accounts/current/invitations/revoke
//
// POST rather than DELETE on an id path, because the console reaches this
// through a plain HTML form and a form can only issue GET or POST.
func (h *Handler) handleRevokeInvitation(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation id is required"})
		return
	}
	invitationID, err := uuid.Parse(strings.TrimSpace(body.ID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invitation id is not valid"})
		return
	}

	requestedAccountID := parseAccountHeader(r)
	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeInternal(w, r, "could not load your workspace", err)
		return
	}

	if err := h.svc.RevokeInvitation(r.Context(), vc.CurrentAccount.ID, viewer, invitationID); err != nil {
		var gateErr *GateError
		switch {
		case AsGateError(err, &gateErr):
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": gateErr.Message,
				"code":  gateErr.Code,
			})
		case errors.Is(err, ErrNotFound):
			// Also the answer for an invitation on another workspace, because
			// the repository scopes its delete by account. A caller learns
			// nothing about invitations it cannot see.
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "this invitation no longer exists",
				"code":  "invitation_not_found",
			})
		default:
			slog.ErrorContext(r.Context(), "accounts: revoke invitation failed",
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "could not revoke the invitation",
			})
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"revoked": true})
}

// handleCreateInvitation implements POST /api/v1/accounts/current/invitations
func (h *Handler) handleCreateInvitation(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email is required"})
		return
	}

	requestedAccountID := parseAccountHeader(r)

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeInternal(w, r, "could not load your workspace", err)
		return
	}

	result, err := h.svc.CreateInvitation(r.Context(), vc.CurrentAccount.ID, viewer, body.Email, body.Role)
	if err != nil {
		var gateErr *GateError
		var capErr *InvitationCapError
		switch {
		case AsInvitationCapError(err, &capErr):
			writeInvitationCap(w, r, capErr)
		case AsGateError(err, &gateErr):
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": gateErr.Message,
				"code":  gateErr.Code,
			})
		case errors.Is(err, ErrInvalidRole):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "role must be owner or member",
				"code":  "invalid_role",
			})
		default:
			slog.ErrorContext(r.Context(), "accounts: create invitation failed",
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "could not create the invitation",
			})
		}
		return
	}

	// delivered and delivery describe what actually happened to the message.
	// The console derives every user-visible string from them, so no surface can
	// report a send that did not occur (issue #1440).
	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         result.ID.String(),
		"email":      result.Email,
		"role":       result.Role,
		"token":      result.Token,
		"expires_at": result.ExpiresAt,
		"delivered":  result.Delivered,
		"delivery":   result.Delivery,
	})
}

// handleUpdateMemberRole implements PATCH /api/v1/accounts/current/members/{user_id}
func (h *Handler) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	rawUserID := strings.Trim(strings.TrimPrefix(r.URL.Path, memberRolePathPrefix), "/")
	targetUserID, err := uuid.Parse(rawUserID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "a valid member id is required",
			"code":  "invalid_user_id",
		})
		return
	}

	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "role is required",
			"code":  "invalid_role",
		})
		return
	}

	vc, err := h.svc.EnsureViewerContext(r.Context(), viewer, parseAccountHeader(r))
	if err != nil {
		slog.ErrorContext(r.Context(), "accounts: viewer context failed",
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "authorization unavailable",
		})
		return
	}

	err = h.svc.UpdateMemberRole(r.Context(), vc.CurrentAccount.ID, viewer, targetUserID, body.Role)
	if err != nil {
		writeMemberRoleError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"user_id": targetUserID.String(),
		"role":    body.Role,
	})
}

// writeMemberRoleError maps a role-change failure onto a status plus a stable
// machine code. Every branch is a truthful, provider-blind reason; internal
// error text is logged, never returned.
func writeMemberRoleError(w http.ResponseWriter, r *http.Request, err error) {
	var gateErr *GateError
	switch {
	case AsGateError(err, &gateErr):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": gateErr.Message,
			"code":  gateErr.Code,
		})
	case errors.Is(err, ErrInvalidRole):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "role must be owner or member",
			"code":  "invalid_role",
		})
	case errors.Is(err, ErrSelfRoleChange):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "you cannot change your own role",
			"code":  "self_role_change_forbidden",
		})
	case errors.Is(err, ErrLastOwner):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "the workspace must keep at least one owner",
			"code":  "last_owner_required",
		})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "that member is not part of this workspace",
			"code":  "member_not_found",
		})
	default:
		slog.ErrorContext(r.Context(), "accounts: update member role failed",
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not update the member role",
		})
	}
}

// handleAcceptInvitation implements POST /api/v1/invitations/accept
func (h *Handler) handleAcceptInvitation(w http.ResponseWriter, r *http.Request) {
	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Token == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token is required"})
		return
	}

	accountID, err := h.svc.AcceptInvitation(r.Context(), viewer, body.Token)
	if err != nil {
		writeAcceptInvitationError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"account_id": accountID.String(),
	})
}

// writeAcceptInvitationError maps an acceptance failure onto a status plus a
// stable machine code so the console can state the real reason (and the real
// next action) instead of always suggesting a fresh link. Internal error text
// is logged, never returned.
func writeAcceptInvitationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrExpired):
		writeJSON(w, http.StatusGone, map[string]string{
			"error": "this invitation has expired",
			"code":  "invitation_expired",
		})
	case errors.Is(err, ErrAlreadyAccepted):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "this invitation has already been accepted",
			"code":  "invitation_already_accepted",
		})
	case errors.Is(err, ErrAlreadyMember):
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "you are already a member of this workspace",
			"code":  "invitation_already_member",
		})
	case errors.Is(err, ErrEmailMismatch):
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "this invitation was sent to a different email address",
			"code":  "invitation_email_mismatch",
		})
	case errors.Is(err, ErrEmailNotVerified):
		// Its own code rather than the shared email_verification_required,
		// which two other handlers also emit for what is usually a role
		// refusal. This is the one refusal in this switch the invitee can
		// actually clear themselves, so the console needs to be able to tell it
		// apart and offer the action that clears it.
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "verify your email address before accepting this invitation",
			"code":  "invitation_email_not_verified",
		})
	// Above the ErrNotFound case on purpose. The invitation was valid and the
	// membership write behind it failed, so telling the invitee their link is
	// not valid would send them chasing a problem they cannot fix.
	case errors.Is(err, ErrMembershipActivation):
		slog.ErrorContext(r.Context(), "accounts: invitation accepted but membership activation failed",
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not accept the invitation",
		})
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "this invitation link is not valid",
			"code":  "invitation_not_found",
		})
	default:
		slog.ErrorContext(r.Context(), "accounts: accept invitation failed",
			slog.String("err", err.Error()))
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "could not accept the invitation",
		})
	}
}

// --- helpers ---

// memberRolePathPrefix is the route prefix for per-member role updates:
// PATCH /api/v1/accounts/current/members/{user_id}
const memberRolePathPrefix = "/api/v1/accounts/current/members/"

func parseAccountHeader(r *http.Request) uuid.UUID {
	val := r.Header.Get("X-Hive-Account-ID")
	if val == "" {
		return uuid.Nil
	}
	id, err := uuid.Parse(val)
	if err != nil {
		return uuid.Nil
	}
	return id
}

// writeInternal logs the real failure and answers with a fixed message.
//
// Nothing from an error string belongs in a response body here. These errors
// carry raw pgx text, and the workspace provisioning path underneath
// EnsureViewerContext can fail on a unique constraint over a slug built from
// the viewer's own name or email local part, which would put both the schema
// detail and that identifier in front of whoever made the request.
func writeInternal(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, slog.String("err", err.Error()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

// writeInvitationCap answers a refusal from the invitation send cap.
//
// The response says the limit was hit and when it lifts, and nothing else. It
// does not name which dimension tripped, because a per-recipient refusal would
// then tell the caller that somebody recently invited that address, and it does
// not vary by whether the address already holds an account, because it never
// looked. The refusal happens before any invitation is stored, so there is
// deliberately no "created but not delivered" result to report.
func writeInvitationCap(w http.ResponseWriter, r *http.Request, capErr *InvitationCapError) {
	if capErr.Unavailable {
		// Not the caller's quota. The counter this cap depends on could not be
		// reached, and an abuse control that cannot count denies rather than
		// admits (#51), so this is a temporary server-side refusal.
		slog.ErrorContext(r.Context(), "accounts: invitation cap counter unavailable",
			slog.String("dimension", capErr.Dimension))
		// A window rollover has nothing to do with a Redis blip, so this does
		// not borrow one: a two second outage must not tell every client to
		// stand off for the rest of the hour.
		w.Header().Set("Retry-After", strconv.Itoa(int(invitationOutageRetry.Seconds())))
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "invitations are temporarily unavailable, please try again shortly",
			"code":  "invitation_unavailable",
		})
		return
	}
	// The real time to the window rollover never reaches the caller, in the
	// header or in the prose. It is a fingerprint of which cap tripped: under
	// five minutes could only be the per-address cooldown, so one invitation
	// from a fresh account would report that somebody, in some other workspace,
	// invited that address moments ago. One constant for every dimension is
	// what makes the refusals indistinguishable. Retry-After is a hint rather
	// than a contract, so a caller that retries at it and is refused again has
	// lost nothing.
	slog.WarnContext(r.Context(), "accounts: invitation refused by send cap",
		slog.String("dimension", capErr.Dimension),
		slog.Duration("actual_retry_after", capErr.RetryAfter))
	w.Header().Set("Retry-After", strconv.Itoa(int(invitationCapRetry.Seconds())))
	writeJSON(w, http.StatusTooManyRequests, map[string]string{
		"error": "invitation limit reached, try again later",
		"code":  "invitation_rate_limited",
	})
}

// What every capped invitation reports, whichever dimension refused it, and
// what a counter outage reports instead: a blip is over in seconds, and telling
// clients to stand off for a window they never hit is its own small outage.
const (
	invitationCapRetry    = 5 * time.Minute
	invitationOutageRetry = 30 * time.Second
)

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// viewerContextResponse converts a ViewerContext to the JSON response shape.
func viewerContextResponse(vc ViewerContext) map[string]interface{} {
	memberships := make([]map[string]interface{}, 0, len(vc.Memberships))
	for _, m := range vc.Memberships {
		memberships = append(memberships, map[string]interface{}{
			"account_id":   m.AccountID.String(),
			"display_name": m.DisplayName,
			"role":         m.Role,
			"status":       m.Status,
		})
	}

	return map[string]interface{}{
		"user": map[string]interface{}{
			"id":             vc.User.ID.String(),
			"email":          vc.User.Email,
			"email_verified": vc.User.EmailVerified,
		},
		"current_account": map[string]interface{}{
			"id":           vc.CurrentAccount.ID.String(),
			"display_name": vc.CurrentAccount.DisplayName,
			"account_type": vc.CurrentAccount.AccountType,
			"role":         vc.CurrentAccount.Role,
		},
		"memberships": memberships,
		// workspace_admin is the tenant-scoped administration signal (issue
		// #1660). current_account.role above answers the billing-account
		// question and the two deliberately disagree for a personal tenant's
		// sole owner, so a client deciding whether to offer feature gates or
		// marketplace reads this one.
		"workspace_admin": vc.WorkspaceAdmin,
		"permissions": func() []string {
			if vc.Permissions == nil {
				return []string{}
			}
			return vc.Permissions
		}(),
	}
}
