package apikeys

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/accounts"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/auth"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/authz"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform"
	"github.com/sakibsadmanshajib/hive/apps/control-plane/internal/platform/db"
)

func errIs(err, target error) bool { return errors.Is(err, target) }

func limitsResponse(l KeyLimits) map[string]interface{} {
	tiers := l.TierOverrides
	if tiers == nil {
		tiers = map[string]TierLimit{}
	}
	tierMap := make(map[string]map[string]int, len(tiers))
	for tier, lim := range tiers {
		tierMap[tier] = map[string]int{"rpm": lim.RPM, "tpm": lim.TPM}
	}
	return map[string]interface{}{
		"api_key_id":     l.APIKeyID.String(),
		"rpm":            l.RPM,
		"tpm":            l.TPM,
		"tier_overrides": tierMap,
	}
}

// Handler handles all API-key HTTP routes.
type Handler struct {
	svc        *Service
	accountSvc *accounts.Service
	roleSvc    *platform.RoleService // optional — used to populate Actor.IsAdmin via IsPlatformAdmin
	policy     authz.Policy
	testVC     *accounts.ViewerContext // non-nil in tests to bypass real accounts service
	testActor  *authz.Actor            // non-nil in tests to supply a canned Actor

	// resolveHealth is optional and, when set, records whether
	// /internal/apikeys/resolve — the endpoint edge-api's whole authorization
	// path depends on — is currently reaching real verdicts or failing on
	// something infrastructural. See platform/db.ResolveHealth.
	resolveHealth *db.ResolveHealth
}

// NewHandler returns a new Handler.
func NewHandler(svc *Service, accountSvc *accounts.Service) *Handler {
	return &Handler{svc: svc, accountSvc: accountSvc, policy: authz.NewPolicy()}
}

// WithResolveHealth returns a copy of the handler wired to record resolve
// outcomes into the given tracker, so /health can reflect runtime pool
// contention rather than only the pool's state at boot.
func (h *Handler) WithResolveHealth(rh *db.ResolveHealth) *Handler {
	cloned := *h
	cloned.resolveHealth = rh
	return &cloned
}

// WithRoleService returns a copy of the handler wired with the platform role
// service so the admin overlay is enabled for Actor construction. Without it,
// Actor.IsAdmin is always false and platform admins cannot manage API keys via
// this handler.
func (h *Handler) WithRoleService(roleSvc *platform.RoleService) *Handler {
	cloned := *h
	cloned.roleSvc = roleSvc
	return &cloned
}

// ServeHTTP dispatches requests to the appropriate sub-handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	base := "/api/v1/accounts/current/api-keys"

	switch {
	case r.Method == http.MethodGet && path == base:
		h.handleListKeys(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/limits"):
		h.handleGetLimits(w, r)
	case r.Method == http.MethodPut && strings.HasSuffix(path, "/limits"):
		h.handleUpdateLimits(w, r)
	case r.Method == http.MethodGet && strings.HasPrefix(path, base+"/"):
		h.handleGetKey(w, r)
	case r.Method == http.MethodPost && path == base:
		h.handleCreateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/policy"):
		h.handleUpdatePolicy(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/rotate"):
		h.handleRotateKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/disable"):
		h.handleDisableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/enable"):
		h.handleEnableKey(w, r)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/revoke"):
		h.handleRevokeKey(w, r)
	case r.Method == http.MethodPost && path == "/internal/apikeys/resolve":
		h.handleInternalResolve(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func (h *Handler) handleGetKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysRead)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

// resolveViewerContext extracts the authenticated viewer and resolves the
// current account, enforcing perm via policy.Can. Read-only routes pass
// authz.PermAPIKeysRead (owner-only, no verification required); mutating
// routes pass authz.PermAPIKeysWrite (owner-only, verified). Gating every
// route on the write permission was the root cause of #683: an unverified
// owner holds api_keys.read and was refused even a 200 on GET.
func (h *Handler) resolveViewerContext(w http.ResponseWriter, r *http.Request, perm authz.Permission) (accounts.ViewerContext, bool) {
	// Test override — bypasses real accounts service in unit tests.
	if h.testVC != nil {
		// Build an Actor from the test ViewerContext and check the policy.
		actor := h.testActor
		if actor == nil {
			a := accounts.ActorFor(
				// Reconstruct a minimal auth.Viewer from testVC fields.
				// Tests that need IsAdmin=true should set testActor directly.
				authFromVC(h.testVC),
				accounts.Membership{
					AccountID: h.testVC.CurrentAccount.ID,
					UserID:    h.testVC.User.ID,
					Role:      h.testVC.CurrentAccount.Role,
					Status:    accounts.StatusActive,
				},
				false,
			)
			actor = &a
		}
		if !h.policy.Can(*actor, perm) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "verified account owner required",
				"code":  "api_key_management_forbidden",
			})
			return accounts.ViewerContext{}, false
		}
		return *h.testVC, true
	}

	viewer, ok := auth.ViewerFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
		return accounts.ViewerContext{}, false
	}

	requestedAccountID := parseAccountHeader(r)
	vc, err := h.accountSvc.EnsureViewerContext(r.Context(), viewer, requestedAccountID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return accounts.ViewerContext{}, false
	}

	// Phase 18: authz via policy.Can(actor, perm).
	// Admin overlay must be reflected in Actor.IsAdmin so platform admins
	// can manage keys regardless of workspace role; hardcoding false would
	// silently deny admin flows.
	isAdmin := false
	if h.roleSvc != nil {
		admin, err := h.roleSvc.IsPlatformAdmin(r.Context(), viewer.UserID)
		if err != nil {
			slog.ErrorContext(r.Context(), "apikeys: platform-admin lookup failed",
				slog.String("user_id", viewer.UserID.String()),
				slog.String("err", err.Error()))
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "authorization unavailable"})
			return accounts.ViewerContext{}, false
		}
		isAdmin = admin
	}
	actor := accounts.ActorFor(viewer, accounts.Membership{
		AccountID: vc.CurrentAccount.ID,
		UserID:    viewer.UserID,
		Role:      vc.CurrentAccount.Role,
		Status:    accounts.StatusActive,
	}, isAdmin)
	if !h.policy.Can(actor, perm) {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "verified account owner required",
			"code":  "api_key_management_forbidden",
		})
		return accounts.ViewerContext{}, false
	}

	return vc, true
}

// authFromVC reconstructs a minimal auth.Viewer from a ViewerContext for test
// Actor construction. Does not include FullName (unused by ActorFor).
func authFromVC(vc *accounts.ViewerContext) auth.Viewer {
	return auth.Viewer{
		UserID:        vc.User.ID,
		Email:         vc.User.Email,
		EmailVerified: vc.User.EmailVerified,
	}
}

func (h *Handler) handleListKeys(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysRead)
	if !ok {
		return
	}

	views, err := h.svc.ListKeyViews(r.Context(), vc.CurrentAccount.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	items := make([]map[string]interface{}, 0, len(views))
	for _, view := range views {
		items = append(items, keyViewItem(view))
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"items": items})
}

// decodeMintBody reads and validates the body shared by the two routes that
// mint a credential, create and rotate. Both used to carry their own copy of
// this block and both were missing the same two checks (issue #1400), so the
// validation lives here once rather than in each caller.
func decodeMintBody(w http.ResponseWriter, r *http.Request) (string, *time.Time, bool) {
	var body struct {
		Nickname  string  `json:"nickname"`
		ExpiresAt *string `json:"expires_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return "", nil, false
	}

	nickname := strings.TrimSpace(body.Nickname)
	if nickname == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nickname is required"})
		return "", nil, false
	}
	if utf8.RuneCountInString(nickname) > MaxNicknameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("nickname must be %d characters or fewer", MaxNicknameLen),
		})
		return "", nil, false
	}

	var expiresAt *time.Time
	if body.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
			return "", nil, false
		}
		// A key whose expiry has already passed is inert the moment it is
		// minted and lists as Expired straight away. Refuse it rather than
		// hand back a credential that never worked.
		if !t.After(time.Now()) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be in the future"})
			return "", nil, false
		}
		expiresAt = &t
	}

	return nickname, expiresAt, true
}

func (h *Handler) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	nickname, expiresAt, ok := decodeMintBody(w, r)
	if !ok {
		return
	}

	input := CreateKeyInput{Nickname: nickname, ExpiresAt: expiresAt}

	result, err := h.svc.CreateKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, input)
	if err != nil {
		if errors.Is(err, ErrAccountNotProvisioned) {
			handleKeyError(w, err)
			return
		}
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, result.Key.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	resp := keyViewItem(view)
	resp["secret"] = result.Secret
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) handleRotateKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	nickname, expiresAt, ok := decodeMintBody(w, r)
	if !ok {
		return
	}

	result, err := h.svc.RotateKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID, nickname, expiresAt)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, result.NewKey.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	newKeyResp := keyViewItem(view)
	newKeyResp["secret"] = result.Secret
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"old_key_id": result.OldKey.ID.String(),
		"new_key":    newKeyResp,
	})
}

func (h *Handler) handleDisableKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.DisableKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleEnableKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.EnableKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	key, err := h.svc.RevokeKey(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	view, err := h.svc.GetKeyView(r.Context(), vc.CurrentAccount.ID, key.ID)
	if err != nil {
		writeInternal(w, r, "request could not be completed", err)
		return
	}

	writeJSON(w, http.StatusOK, keyViewItem(view))
}

func (h *Handler) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}

	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	var body struct {
		ExpiresAt          *string  `json:"expires_at"`
		AllowAllModels     *bool    `json:"allow_all_models"`
		AllowedGroupNames  []string `json:"allowed_group_names"`
		AllowedAliases     []string `json:"allowed_aliases"`
		DeniedAliases      []string `json:"denied_aliases"`
		BudgetKind         *string  `json:"budget_kind"`
		BudgetLimitCredits *int64   `json:"budget_limit_credits"`
		BudgetAnchorAt     *string  `json:"budget_anchor_at"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	input := UpdatePolicyInput{
		AllowAllModels:     body.AllowAllModels,
		AllowedGroupNames:  body.AllowedGroupNames,
		AllowedAliases:     body.AllowedAliases,
		DeniedAliases:      body.DeniedAliases,
		BudgetKind:         body.BudgetKind,
		BudgetLimitCredits: body.BudgetLimitCredits,
	}

	if body.ExpiresAt != nil {
		t, err := time.Parse(time.RFC3339, *body.ExpiresAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expires_at must be RFC3339"})
			return
		}
		input.ExpiresAt = &t
	}
	if body.BudgetAnchorAt != nil {
		t, err := time.Parse(time.RFC3339, *body.BudgetAnchorAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "budget_anchor_at must be RFC3339"})
			return
		}
		input.BudgetAnchorAt = &t
	}

	policy, err := h.svc.UpdatePolicy(r.Context(), vc.CurrentAccount.ID, vc.User.ID, keyID, input)
	if err != nil {
		handleKeyError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"api_key_id":       policy.APIKeyID.String(),
		"allow_all_models": policy.AllowAllModels,
		"budget_kind":      policy.BudgetKind,
		"policy_version":   policy.PolicyVersion,
	})
}

func (h *Handler) handleGetLimits(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysRead)
	if !ok {
		return
	}
	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}
	limits, err := h.svc.GetLimits(r.Context(), vc.CurrentAccount.ID, keyID)
	if err != nil {
		handleKeyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limitsResponse(limits))
}

func (h *Handler) handleUpdateLimits(w http.ResponseWriter, r *http.Request) {
	vc, ok := h.resolveViewerContext(w, r, authz.PermAPIKeysWrite)
	if !ok {
		return
	}
	keyID, ok := extractKeyID(w, r)
	if !ok {
		return
	}

	var body struct {
		RPM           int                  `json:"rpm"`
		TPM           int                  `json:"tpm"`
		TierOverrides map[string]TierLimit `json:"tier_overrides"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}

	limits, err := h.svc.UpdateLimits(r.Context(), vc.CurrentAccount.ID, keyID, KeyLimitsInput{
		RPM:           body.RPM,
		TPM:           body.TPM,
		TierOverrides: body.TierOverrides,
	})
	if err != nil {
		switch {
		case errIs(err, ErrLimitsOutOfRange):
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "rate-limit value out of range",
				"code":  "limits_out_of_range",
			})
		default:
			handleKeyError(w, err)
		}
		return
	}

	writeJSON(w, http.StatusOK, limitsResponse(limits))
}

func (h *Handler) handleInternalResolve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TokenHash string `json:"token_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TokenHash == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "token_hash is required"})
		return
	}

	snapshot, err := h.svc.ResolveSnapshot(r.Context(), body.TokenHash)
	if err != nil {
		if h.resolveHealth != nil {
			if isKeyVerdictError(err) {
				// A real verdict (key not found, revoked, disabled, expired)
				// means the database answered; this is not a pool problem.
				h.resolveHealth.RecordSuccess()
			} else {
				h.resolveHealth.RecordFailure()
			}
		}
		handleKeyError(w, err)
		return
	}

	if h.resolveHealth != nil {
		h.resolveHealth.RecordSuccess()
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// isKeyVerdictError reports whether err is one of the sentinel errors that
// mean control-plane reached a real, database-backed answer about the key,
// as opposed to an infrastructural failure (pool checkout timeout,
// connection error) that never reached one. Mirrors handleKeyError's own
// classification, kept separate rather than folded into it: handleKeyError
// backs every apikeys route, and a failure on, say, updating a key's limits
// says nothing about whether resolve — the one endpoint edge-api's
// authorization path depends on — is healthy.
func isKeyVerdictError(err error) bool {
	return errIs(err, ErrNotFound) || errIs(err, ErrRevoked) || errIs(err, ErrDisabled) || errIs(err, ErrNotActive)
}

// --- helpers ---

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

// writeInternal logs the real failure and answers with a fixed message. No
// error string from this package belongs in a response body: it carries raw pgx
// text, and the workspace provisioning inside EnsureViewerContext can fail on a
// unique constraint over a slug built from the viewer's own name or email local
// part.
func writeInternal(w http.ResponseWriter, r *http.Request, msg string, err error) {
	slog.ErrorContext(r.Context(), msg, slog.String("err", err.Error()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

// writeOpaque is writeInternal for the error mappers that do not carry the
// request. Same contract: the real error goes to the log, a fixed message goes
// to the client.
func writeOpaque(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, slog.String("err", err.Error()))
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// extractKeyID parses the key_id segment from the request path.
// Expected path: /api/v1/accounts/current/api-keys/{key_id}/{action}
func extractKeyID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	// Path: /api/v1/accounts/current/api-keys/{key_id}/{action}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/accounts/current/api-keys/"), "/")
	if len(parts) < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key_id required"})
		return uuid.Nil, false
	}
	id, err := uuid.Parse(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid key_id"})
		return uuid.Nil, false
	}
	return id, true
}

// accountNotProvisionedMessage is the customer-facing half of the issue #1330
// fix. The refusal is only an improvement on the old behaviour if it says what
// is missing and what to do about it, so it names the missing link, echoes the
// exact code edge-api would have answered with (which is what joins a customer
// report to the operator log line PR #1240 added), and gives the two remedies
// that actually exist. It carries no account, tenant or key identifier: this
// text is rendered in a browser.
// It deliberately does NOT say "reload and it will sort itself out". The only
// principal that can reach this refusal is one whose token already carries a
// tenant claim (a user with no claim at all is redirected into
// /console/provision before any page renders), and nothing on the console's
// render path re-attempts the mapping for that user: EnsureViewerContext only
// provisions when there are zero memberships, and the reconciler sweep only
// looks at identities holding no tenant membership. Telling them to retry
// would be a loop that cannot terminate.
const accountNotProvisionedMessage = "This workspace has no billing account linked, so a key created here would be rejected by the API with account_not_provisioned. Only one workspace per user can carry the billing link: if you have another workspace, create the key there instead. If this is your only workspace, it has to be linked before it can issue keys."

// accountNotProvisionedCode is the stable machine code for the refusal above.
// It is deliberately the same string edge-api answers with when the key is
// used, so a customer report, this refusal and the operator log line PR #1240
// added all name one thing.
const accountNotProvisionedCode = "account_not_provisioned"

func handleKeyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
	case errors.Is(err, ErrRevoked):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is revoked"})
	case errors.Is(err, ErrDisabled):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not disabled"})
	case errors.Is(err, ErrNotActive):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "key is not active"})
	case errors.Is(err, ErrAccountNotProvisioned):
		// The machine code matters as much as the sentence. The console's
		// proxy route deliberately never forwards upstream error text to the
		// browser (app/api/v1/accounts/current/[...path]/route.ts), so without
		// a code the refusal reaches a customer as the word "Conflict"; with
		// one, the console renders its own wording for this exact refusal, the
		// way it already does for last_owner_required. The sentence is still
		// carried for callers hitting this API directly.
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": accountNotProvisionedMessage,
			"code":  accountNotProvisionedCode,
		})
	default:
		writeOpaque(w, "request could not be completed", err)
	}
}

func keyViewItem(view KeyView) map[string]interface{} {
	item := map[string]interface{}{
		"id":              view.ID.String(),
		"nickname":        view.Nickname,
		"status":          string(view.Status),
		"redacted_suffix": view.RedactedSuffix,
		"created_at":      view.CreatedAt.Format(time.RFC3339),
		"updated_at":      view.UpdatedAt.Format(time.RFC3339),
		"expires_at":      formatTimestamp(view.ExpiresAt),
		"last_used_at":    formatTimestamp(view.LastUsedAt),
		"expiration_summary": map[string]interface{}{
			"kind":  view.ExpirationSummary.Kind,
			"label": view.ExpirationSummary.Label,
		},
		"budget_summary": map[string]interface{}{
			"kind":  view.BudgetSummary.Kind,
			"label": view.BudgetSummary.Label,
		},
		"allowlist_summary": map[string]interface{}{
			"mode":        view.AllowlistSummary.Mode,
			"group_names": view.AllowlistSummary.GroupNames,
			"label":       view.AllowlistSummary.Label,
		},
		// spend_credits and budget_limit_credits are raw integers for the
		// console to format (lib/format/model-pricing.ts) and, for the limit,
		// to pre-fill an edit form with. budget_summary.label already carries
		// a human sentence version of the limit but no machine-readable value
		// a UI could edit against.
		"spend_credits":        view.SpendCredits,
		"budget_limit_credits": view.BudgetLimitCredits,
		// budget_spend_credits is the counter edge-api enforces against, null
		// when the key carries no cap. spend_credits above is the lifetime
		// rollup, and the two diverge by whatever the key spent before it was
		// capped, so a console drawing a proportion of the cap has to divide
		// this one or it will report a refusal that is not happening.
		"budget_spend_credits": view.BudgetSpendCredits,
	}
	return item
}

// formatTimestamp formats a time pointer for JSON response, returning nil for nil.
func formatTimestamp(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return t.Format(time.RFC3339)
}

// keyIDFromPath extracts a UUID from the second-to-last path segment.
func keyIDFromPath(path, base string) (uuid.UUID, error) {
	suffix := strings.TrimPrefix(path, base+"/")
	parts := strings.SplitN(suffix, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return uuid.Nil, fmt.Errorf("missing key_id")
	}
	return uuid.Parse(parts[0])
}
