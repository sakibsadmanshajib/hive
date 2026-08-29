package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines the data-access interface for accounts.
// The concrete pgxRepository uses pgx/v5 against Supabase Postgres.
type Repository interface {
	ListMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]Membership, error)
	ActiveTenantID(ctx context.Context, userID uuid.UUID) (uuid.UUID, bool, error)
	CreateAccount(ctx context.Context, acct Account) error
	CreateMembership(ctx context.Context, m Membership) error
	CreateProfile(ctx context.Context, p AccountProfile) error
	// ProvisionDefaultWorkspace atomically provisions acct/membership/profile
	// for a first-time viewer, serialized against every other concurrent call
	// for the same viewer. See provisionDefaultWorkspace in service.go for why
	// this must be atomic rather than three independent Create* calls.
	// wonElsewhere is true when a concurrent call for this same viewer had
	// already committed a workspace by the time this call took its lock: in
	// that case nothing is inserted and existingAccountID is the winner's.
	ProvisionDefaultWorkspace(ctx context.Context, acct Account, membership Membership, profile AccountProfile) (existingAccountID uuid.UUID, wonElsewhere bool, err error)
	GetAccountByID(ctx context.Context, id uuid.UUID) (*Account, error)
	CreateInvitation(ctx context.Context, inv Invitation) error
	FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error)
	AcceptInvitation(ctx context.Context, invitationID uuid.UUID, acceptedAt time.Time) error
	ListMembersByAccountID(ctx context.Context, accountID uuid.UUID) ([]Member, error)
	UpdateMembershipRole(ctx context.Context, accountID, userID uuid.UUID, role string) error
	ActivateMembership(ctx context.Context, accountID, userID uuid.UUID, role string) error
}

// pgxRepository is the production implementation backed by Supabase Postgres.
type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository returns a Repository backed by the given pgx pool.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// withActorTx runs fn inside an explicit transaction with the RLS session
// variable app.current_actor_user_id set LOCAL to actorUserID (issue #896).
// hive_app is NOT BYPASSRLS, and public.account_memberships' hive_app policy
// (20260829_04_account_memberships_hive_app_scope.sql) is keyed on this
// setting for every "my own memberships" access shape: ListMembershipsByUserID,
// CreateMembership, ActivateMembership, and the account_memberships reads and
// writes inside ProvisionDefaultWorkspace.
//
// Posture caveat (issue #1444), the canonical statement of it for this
// package: that policy does not bind on the system as currently deployed, and
// this scoping does not make it bind. hive_app is NOLOGIN with zero role
// members, no production code path anywhere in this repository issues
// SET ROLE hive_app, and control-plane connects as postgres, which is
// BYPASSRLS and skips policy evaluation entirely. So the Go predicates in the
// callers below remain the only tenancy enforcement today, exactly as before.
// What the session scoping buys is that the policy is correct and exercised
// by tests ahead of the connection-posture change that makes it load bearing;
// issue #1444 owns that change and is the blocker. Full evidence in the header
// of supabase/migrations/20260829_04_account_memberships_hive_app_scope.sql.
// Read every "hive_app is NOT BYPASSRLS" remark in this package as a statement
// about the role's attributes, never as evidence that hive_app connects.
//
// LOCAL scope inside an explicit transaction is required, not incidental --
// see egress/repository.go's withTenantTx comment for the two ways this was
// gotten wrong before: a bare Exec followed by a separate Query loses the
// LOCAL setting the instant the Exec's own implicit transaction ends, and
// session scope (is_local=false) survives pool.Release and leaks the actor
// onto whichever request borrows that physical connection next.
func (r *pgxRepository) withActorTx(ctx context.Context, actorUserID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("accounts: begin actor-scoped tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_user_id', $1, true)", actorUserID.String()); err != nil {
		return fmt.Errorf("accounts: set actor scope: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("accounts: commit actor-scoped tx: %w", err)
	}
	return nil
}

// withAccountTx is withActorTx's counterpart for the "members of one account
// I already administer" access shape (issue #896): app.current_account_id,
// read by the same migration's second hive_app policy, and subject to the same
// issue #1444 posture caveat spelled out on withActorTx above. Used by
// ListMembersByAccountID (the member-list page, which by design reads every
// member's row, not just the caller's own) and UpdateMembershipRole
// (including its embedded last-owner-count subquery, which reads every owner
// row of the account, and its post-failure diagnostic read).
func (r *pgxRepository) withAccountTx(ctx context.Context, accountID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("accounts: begin account-scoped tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_account_id', $1, true)", accountID.String()); err != nil {
		return fmt.Errorf("accounts: set account scope: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("accounts: commit account-scoped tx: %w", err)
	}
	return nil
}

// ListMembershipsByUserID returns userID's memberships, active and invited
// alike, ordered by created_at then id.
//
// The absence of a status predicate here is deliberate: this is a listing
// primitive, not an authorization primitive. AcceptInvitation needs the invited
// row so it can activate it rather than dead-ending on it. Callers making an
// access decision filter with activeMemberships instead, and the two consumers
// on the console side (the account switch route and the workspace switcher)
// filter on the status this list reports. An invited row grants nothing
// anywhere.
//
// Without an explicit ORDER BY, Postgres may return rows for the
// same user in a different order across calls (heap/index layout shifts on
// UPDATE, e.g. every account_memberships upsert), which made
// EnsureViewerContext's "first membership is the default workspace" pick
// nondeterministic for any user who belongs to more than one account. id is
// a tiebreaker only for the pathological case of two memberships sharing one
// created_at (a single bulk upsert statement stamps every row it inserts
// with the same transaction timestamp).
func (r *pgxRepository) ListMembershipsByUserID(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	var memberships []Membership
	err := r.withActorTx(ctx, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, account_id, user_id, role, status, created_at
			FROM public.account_memberships
			WHERE user_id = $1
			ORDER BY created_at ASC, id ASC
		`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m Membership
			if err := rows.Scan(&m.ID, &m.AccountID, &m.UserID, &m.Role, &m.Status, &m.CreatedAt); err != nil {
				return err
			}
			memberships = append(memberships, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return memberships, nil
}

// ActiveTenantID reports the tenant userID holds an ACTIVE tenant_users row
// on (joined to a non-archived tenant), if any. Mirrors the predicate
// signup.Provisioner.activeMembership and public.custom_access_token_hook
// use, so "has a tenant" means the same thing everywhere. Used opportunistically
// by provisionDefaultWorkspace to retry the tenant billing account mapping
// once the account side of that pairing exists — see
// signup.EnsureTenantBillingAccount's doc for why both sides need to try.
//
// A user who belongs to more than one tenant (an edge case, not the common
// "one org = one tenant" shape) gets an arbitrary one of them here: this is a
// best-effort retry, not the sole mapping mechanism, so missing a second
// tenant on that rare path is not a correctness gap — the backfill migrations
// and signup.Provisioner's own call remain the paths of record.
func (r *pgxRepository) ActiveTenantID(ctx context.Context, userID uuid.UUID) (uuid.UUID, bool, error) {
	var tenantID uuid.UUID
	err := r.pool.QueryRow(ctx, `
		SELECT tu.tenant_id
		  FROM public.tenant_users tu
		  JOIN public.tenants t ON t.id = tu.tenant_id
		 WHERE tu.user_id = $1
		   AND tu.status = 'ACTIVE'
		   AND t.archived_at IS NULL
		 LIMIT 1
	`, userID).Scan(&tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return tenantID, true, nil
}

// accountsSlugUniqueConstraint is the Postgres-assigned name for accounts'
// column-level `slug ... unique` constraint (supabase/migrations/
// 20260328_01_identity_foundation.sql), i.e. Postgres's default
// <table>_<column>_key naming, never given an explicit name in the
// migration. Matched by name, not just by SQLSTATE 23505, so a future
// unique constraint added to this table is never misreported as
// ErrSlugTaken.
const accountsSlugUniqueConstraint = "accounts_slug_key"

// CreateAccount inserts one account row. provisionDefaultWorkspace does not
// call this directly — see ProvisionDefaultWorkspace below, which needs the
// insert inside its own locked transaction. UNIQUE (slug) still surfaces
// here as ErrSlugTaken for any other caller; see its doc comment in types.go.
func (r *pgxRepository) CreateAccount(ctx context.Context, acct Account) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		VALUES ($1, $2, $3, $4, $5)
	`, acct.ID, acct.Slug, acct.DisplayName, acct.AccountType, acct.OwnerUserID)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == accountsSlugUniqueConstraint {
		return ErrSlugTaken
	}
	return err
}

// CreateMembership inserts one membership row.
//
// UNIQUE (account_id, user_id) is what arbitrates two concurrent first-time
// acceptances of the same invitation: one row is written and the other insert
// is rejected. The loser is not a server fault, it is a caller who is a member
// as of that instant, so the unique violation is translated into ErrAlreadyMember
// rather than escaping as raw pgx text and being answered as an opaque 500.
func (r *pgxRepository) CreateMembership(ctx context.Context, m Membership) error {
	err := r.withActorTx(ctx, m.UserID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO public.account_memberships (id, account_id, user_id, role, status)
			VALUES ($1, $2, $3, $4, $5)
		`, m.ID, m.AccountID, m.UserID, m.Role, m.Status)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation {
		return ErrAlreadyMember
	}
	return err
}

func (r *pgxRepository) CreateProfile(ctx context.Context, p AccountProfile) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_profiles (account_id, owner_name, login_email, profile_setup_complete)
		VALUES ($1, $2, $3, $4)
	`, p.AccountID, p.OwnerName, p.LoginEmail, p.ProfileSetupComplete)
	return err
}

// ProvisionDefaultWorkspace creates acct, membership, and profile inside one
// transaction guarded by a pg_advisory_xact_lock keyed on membership.UserID.
// Two concurrent calls for the same viewer serialize on that lock — the
// second does not even start its re-check until the first has committed or
// rolled back — so the TOCTOU window a bare "list memberships, then insert"
// leaves open (a second Server Component request for a brand-new viewer
// deciding independently that no workspace exists yet) cannot produce two
// personal workspaces for one viewer. It can still return ErrSlugTaken: that
// signals a *different* viewer's display name collapsed to the same slug
// (buildSlug has no per-user uniqueifier), which this lock does not
// serialize against (different key) and provisionDefaultWorkspace in
// service.go handles by retrying with a de-duplicated slug.
func (r *pgxRepository) ProvisionDefaultWorkspace(ctx context.Context, acct Account, membership Membership, profile AccountProfile) (existingAccountID uuid.UUID, wonElsewhere bool, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: begin provisioning tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Issue #896: every account_memberships statement below runs as the
	// caller's own actor scope (Shape A -- see withActorTx's doc comment).
	// hive_app is NOT BYPASSRLS, and the account_memberships hive_app policy
	// (20260829_04_account_memberships_hive_app_scope.sql) requires this
	// setting for both the pre-check read and the owner-membership insert.
	// Dormant today for the reason withActorTx documents (issue #1444).
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_actor_user_id', $1, true)", membership.UserID.String()); err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: set actor scope: %w", err)
	}

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1)::int8)`, membership.UserID.String()); err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: acquire provisioning lock: %w", err)
	}

	// Re-check now that the lock is held: a concurrent transaction for this
	// same viewer may have committed and released the lock while this one was
	// waiting to acquire it.
	var winnerAccountID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT account_id FROM public.account_memberships
		WHERE user_id = $1 AND status = $2
		ORDER BY created_at ASC, id ASC
		LIMIT 1
	`, membership.UserID, StatusActive).Scan(&winnerAccountID)
	if err == nil {
		return winnerAccountID, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, fmt.Errorf("accounts: check active membership under lock: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.accounts (id, slug, display_name, account_type, owner_user_id)
		VALUES ($1, $2, $3, $4, $5)
	`, acct.ID, acct.Slug, acct.DisplayName, acct.AccountType, acct.OwnerUserID); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgerrcode.UniqueViolation && pgErr.ConstraintName == accountsSlugUniqueConstraint {
			return uuid.Nil, false, ErrSlugTaken
		}
		return uuid.Nil, false, fmt.Errorf("accounts: create default account: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.account_memberships (id, account_id, user_id, role, status)
		VALUES ($1, $2, $3, $4, $5)
	`, membership.ID, membership.AccountID, membership.UserID, membership.Role, membership.Status); err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: create owner membership: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO public.account_profiles (account_id, owner_name, login_email, profile_setup_complete)
		VALUES ($1, $2, $3, $4)
	`, profile.AccountID, profile.OwnerName, profile.LoginEmail, profile.ProfileSetupComplete); err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: create account profile: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, false, fmt.Errorf("accounts: commit provisioning tx: %w", err)
	}
	return acct.ID, false, nil
}

func (r *pgxRepository) GetAccountByID(ctx context.Context, id uuid.UUID) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, slug, display_name, account_type, owner_user_id, created_at, updated_at
		FROM public.accounts
		WHERE id = $1
	`, id)

	var a Account
	if err := row.Scan(&a.ID, &a.Slug, &a.DisplayName, &a.AccountType, &a.OwnerUserID, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, ErrNotFound
	}
	return &a, nil
}

func (r *pgxRepository) CreateInvitation(ctx context.Context, inv Invitation) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO public.account_invitations
		  (id, account_id, email, role, token_hash, expires_at, invited_by_user_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, inv.ID, inv.AccountID, inv.Email, inv.Role, inv.TokenHash, inv.ExpiresAt, inv.InvitedByUserID)
	return err
}

func (r *pgxRepository) FindInvitationByTokenHash(ctx context.Context, tokenHash string) (*Invitation, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, account_id, email, role, token_hash, expires_at, accepted_at, invited_by_user_id, created_at
		FROM public.account_invitations
		WHERE token_hash = $1
	`, tokenHash)

	var inv Invitation
	if err := row.Scan(&inv.ID, &inv.AccountID, &inv.Email, &inv.Role, &inv.TokenHash,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.InvitedByUserID, &inv.CreatedAt); err != nil {
		return nil, ErrNotFound
	}
	return &inv, nil
}

// AcceptInvitation consumes an invitation exactly once.
//
// The accepted_at IS NULL predicate makes the write the arbiter of the race the
// service's own AcceptedAt read cannot settle: a double-clicked link sends two
// requests that both read an unaccepted invitation, and only one of them may
// consume it. The loser matches no row and gets ErrAlreadyAccepted, which its
// caller treats as benign because the membership write already succeeded.
func (r *pgxRepository) AcceptInvitation(ctx context.Context, invitationID uuid.UUID, acceptedAt time.Time) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE public.account_invitations
		SET accepted_at = $1
		WHERE id = $2
		  AND accepted_at IS NULL
	`, acceptedAt, invitationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrAlreadyAccepted
	}
	return nil
}

// ActivateMembership turns an invited membership row into an active one and
// stamps the role the invitation granted.
//
// Two properties the WHERE and the CASE carry between them. The row must
// already exist, so this cannot resurrect a member whose row was deleted. The
// role is rewritten only while the row is still invited, so an active member's
// role cannot be re-stamped by a replayed acceptance. Matching an already
// active row rather than excluding it makes the call idempotent, which is what
// keeps a concurrent second acceptance from reporting failure for a seat that
// is in fact active.
//
// Zero rows affected therefore means there is no membership row at all, which
// surfaces as ErrNotFound.
//
// The WHERE names the two statuses it is willing to write over rather than
// trusting the CHECK constraint to keep the set at two. A comment is not a
// guard, and the defect this repository is fixing was itself a query that was
// correct only while an assumption about the data held. A later suspension or
// soft-removal status now falls through as ErrNotFound instead of being
// silently reinstated by an accepted invitation.
func (r *pgxRepository) ActivateMembership(ctx context.Context, accountID, userID uuid.UUID, role string) error {
	var rowsAffected int64
	err := r.withActorTx(ctx, userID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE public.account_memberships
			SET role = CASE WHEN status = 'invited' THEN $3 ELSE role END,
			    status = 'active'
			WHERE account_id = $1
			  AND user_id = $2
			  AND status IN ('active', 'invited')
		`, accountID, userID, role)
		if err != nil {
			return err
		}
		rowsAffected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return fmt.Errorf("accounts: activate membership: %w", err)
	}
	if rowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// ListMembersByAccountID returns the account's memberships joined to the
// member's auth.users email, so callers can render a human identity instead of
// a bare UUID. The join is a LEFT JOIN and email is coalesced: a membership
// whose auth row has no email still lists.
func (r *pgxRepository) ListMembersByAccountID(ctx context.Context, accountID uuid.UUID) ([]Member, error) {
	var members []Member
	err := r.withAccountTx(ctx, accountID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT m.user_id, coalesce(u.email, ''), m.role, m.status
			FROM public.account_memberships m
			LEFT JOIN auth.users u ON u.id = m.user_id
			WHERE m.account_id = $1
			ORDER BY m.created_at
		`, accountID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var m Member
			if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.Status); err != nil {
				return err
			}
			members = append(members, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return members, nil
}

// UpdateMembershipRole writes a new role onto an active membership.
//
// The statement carries its own last-owner guard: demoting an owner only
// matches while another active owner exists. The service checks the same
// invariant first (so the caller gets a precise error), but two concurrent
// demotions could each pass that read-then-write check, and an ownerless
// workspace is unrecoverable without support. Zero rows affected therefore
// means "not an active member, or this write would have removed the last
// owner", and surfaces as ErrNotFound / ErrLastOwner respectively.
func (r *pgxRepository) UpdateMembershipRole(ctx context.Context, accountID, userID uuid.UUID, role string) error {
	// Shape B (issue #896): the target row's user_id is the member being
	// changed, not necessarily the caller, and the embedded last-owner-count
	// subquery reads every owner row of the account, not just one user's. Both
	// are scoped by account, not by actor.
	return r.withAccountTx(ctx, accountID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE public.account_memberships AS m
			SET role = $3
			WHERE m.account_id = $1
			  AND m.user_id = $2
			  AND m.status = 'active'
			  AND (
			    $3 = 'owner'
			    OR m.role <> 'owner'
			    OR (
			      SELECT count(*) FROM public.account_memberships o
			      WHERE o.account_id = $1 AND o.role = 'owner' AND o.status = 'active'
			    ) > 1
			  )
		`, accountID, userID, role)
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			return nil
		}
		// Distinguish the two zero-row causes so the HTTP layer stays truthful.
		var currentRole string
		var activeOwners int
		if scanErr := tx.QueryRow(ctx, `
			SELECT m.role,
			       (SELECT count(*) FROM public.account_memberships o
			        WHERE o.account_id = $1 AND o.role = 'owner' AND o.status = 'active')
			FROM public.account_memberships m
			WHERE m.account_id = $1 AND m.user_id = $2 AND m.status = 'active'
		`, accountID, userID).Scan(&currentRole, &activeOwners); scanErr != nil {
			return ErrNotFound
		}
		if currentRole == RoleOwner && role != RoleOwner && activeOwners <= 1 {
			return ErrLastOwner
		}
		return ErrNotFound
	})
}
