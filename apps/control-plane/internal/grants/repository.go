package grants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository is the narrow data-access port for the grants service.
//
// Note: this interface is INTENTIONALLY missing Update and Delete methods.
// `credit_grants` is append-only at schema (BEFORE UPDATE OR DELETE trigger
// on the table raises 'integrity_constraint_violation'); the absence of
// these methods is the application-layer mirror of that schema discipline.
type Repository interface {
	// CreateWithLedger executes the atomic same-tx insert: a credit_grants
	// row + a credit_ledger_entries row + a credit_idempotency_keys row.
	// Both writes share a single pgx transaction. On any error, the entire
	// tx is rolled back — the grant row is absent and no credits are
	// posted.
	CreateWithLedger(ctx context.Context, input CreateInput) (CreateResult, error)

	// Get returns the grant by id, scoped to "viewable by anyone who knows
	// the id" — handler enforces caller authorisation (admin OR self).
	Get(ctx context.Context, id uuid.UUID) (CreditGrant, error)

	// List returns grants matching filter, keyset-paginated.
	List(ctx context.Context, filter ListFilter) ([]CreditGrant, error)
}

type pgxRepository struct {
	pool *pgxpool.Pool
}

// NewPgxRepository constructs a pgxpool-backed Repository.
func NewPgxRepository(pool *pgxpool.Pool) Repository {
	return &pgxRepository{pool: pool}
}

// CreateWithLedger atomically inserts the grant row + ledger entry. The
// idempotency_key on the ledger entry mirrors the grant id (deterministic;
// re-running the same input yields the same ledger row id via the unique
// (account_id, operation_type, idempotency_key) constraint on
// credit_idempotency_keys).
func (r *pgxRepository) CreateWithLedger(ctx context.Context, input CreateInput) (CreateResult, error) {
	if input.AmountBDTSubunits == nil || input.AmountBDTSubunits.Sign() <= 0 {
		return CreateResult{}, ErrInvalidAmount
	}
	if input.GrantedToUserID == uuid.Nil || input.GrantedToWorkspaceID == uuid.Nil {
		return CreateResult{}, ErrInvalidGrantee
	}

	// Validate that subunits fits int64 (BDT subunits column is bigint).
	if !input.AmountBDTSubunits.IsInt64() {
		return CreateResult{}, fmt.Errorf("grants: amount overflows int64 bigint storage")
	}
	creditsDelta := input.AmountBDTSubunits.Int64()

	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	idempotencyKey := input.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = "grant:" + uuid.New().String()
	}

	// Step 1: claim idempotency key. ON CONFLICT DO NOTHING so duplicate
	// calls with the same key short-circuit.
	cmd, err := tx.Exec(ctx, `
		INSERT INTO public.credit_idempotency_keys
			(account_id, operation_type, idempotency_key, request_id, attempt_id)
		VALUES ($1, 'grant', $2, NULL, NULL)
		ON CONFLICT DO NOTHING
	`, input.GrantedToWorkspaceID, idempotencyKey)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: insert idempotency key: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return CreateResult{}, fmt.Errorf("grants: duplicate idempotency_key %q", idempotencyKey)
	}

	// Step 2: insert the ledger entry (entry_type='grant', positive credits).
	metadata := map[string]any{
		"reason_note":         input.ReasonNote,
		"granted_by_user_id":  input.GrantedByUserID.String(),
		"granted_to_user_id":  input.GrantedToUserID.String(),
		"source":              "discretionary_grant",
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: marshal metadata: %w", err)
	}

	var ledgerID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO public.credit_ledger_entries
			(account_id, entry_type, credits_delta, idempotency_key, request_id, attempt_id, reservation_id, metadata)
		VALUES ($1, 'grant', $2, $3, NULL, NULL, NULL, $4::jsonb)
		RETURNING id
	`, input.GrantedToWorkspaceID, creditsDelta, idempotencyKey, metadataBytes).Scan(&ledgerID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: insert ledger entry: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE public.credit_idempotency_keys
		SET ledger_entry_id = $3
		WHERE account_id = $1 AND operation_type = 'grant' AND idempotency_key = $2
	`, input.GrantedToWorkspaceID, idempotencyKey, ledgerID)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: update idempotency key: %w", err)
	}

	// Step 3: insert the credit_grants audit row (immutable). FK on
	// ledger_entry_id ties the grant to its ledger entry so future
	// reconciliation has both halves.
	var grantRow CreditGrant
	var amountInt64 int64
	err = tx.QueryRow(ctx, `
		INSERT INTO public.credit_grants
			(granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
			 amount_bdt_subunits, reason_note, ledger_entry_id, currency)
		VALUES ($1, $2, $3, $4, $5, $6, 'BDT')
		RETURNING id, granted_by_user_id, granted_to_user_id,
		          granted_to_workspace_id, amount_bdt_subunits, COALESCE(reason_note,''),
		          ledger_entry_id, currency, created_at
	`, input.GrantedByUserID, input.GrantedToUserID, input.GrantedToWorkspaceID,
		creditsDelta, nullableString(input.ReasonNote), ledgerID).Scan(
		&grantRow.ID,
		&grantRow.GrantedByUserID,
		&grantRow.GrantedToUserID,
		&grantRow.GrantedToWorkspaceID,
		&amountInt64,
		&grantRow.ReasonNote,
		&grantRow.LedgerEntryID,
		&grantRow.Currency,
		&grantRow.CreatedAt,
	)
	if err != nil {
		return CreateResult{}, fmt.Errorf("grants: insert grant row: %w", err)
	}
	grantRow.AmountBDTSubunits = new(big.Int).SetInt64(amountInt64)

	if err := tx.Commit(ctx); err != nil {
		return CreateResult{}, fmt.Errorf("grants: commit tx: %w", err)
	}

	return CreateResult{Grant: grantRow, LedgerEntryID: ledgerID}, nil
}

// Get returns a single grant. ErrNotFound on miss.
func (r *pgxRepository) Get(ctx context.Context, id uuid.UUID) (CreditGrant, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
		       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id,
		       currency, created_at
		FROM public.credit_grants
		WHERE id = $1
	`, id)
	return scanGrant(row)
}

// List runs a keyset-paginated query against credit_grants matching filter.
func (r *pgxRepository) List(ctx context.Context, filter ListFilter) ([]CreditGrant, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	// Build query — exactly one of (grantor, grantee user, grantee
	// workspace) is required by callers.
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case filter.GrantorUserID != uuid.Nil:
		if filter.Cursor != nil {
			rows, err = r.pool.Query(ctx, `
				SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
				       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id, currency, created_at
				FROM public.credit_grants
				WHERE granted_by_user_id = $1 AND id < $2
				ORDER BY created_at DESC, id DESC
				LIMIT $3`, filter.GrantorUserID, filter.Cursor, limit)
		} else {
			rows, err = r.pool.Query(ctx, `
				SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
				       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id, currency, created_at
				FROM public.credit_grants
				WHERE granted_by_user_id = $1
				ORDER BY created_at DESC, id DESC
				LIMIT $2`, filter.GrantorUserID, limit)
		}
	case filter.GranteeUserID != uuid.Nil:
		rows, err = r.pool.Query(ctx, `
			SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
			       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id, currency, created_at
			FROM public.credit_grants
			WHERE granted_to_user_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, filter.GranteeUserID, limit)
	case filter.GranteeWorkspaceID != uuid.Nil:
		rows, err = r.pool.Query(ctx, `
			SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
			       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id, currency, created_at
			FROM public.credit_grants
			WHERE granted_to_workspace_id = $1
			ORDER BY created_at DESC, id DESC
			LIMIT $2`, filter.GranteeWorkspaceID, limit)
	default:
		// List-all (admin GET /v1/admin/credit-grants).
		rows, err = r.pool.Query(ctx, `
			SELECT id, granted_by_user_id, granted_to_user_id, granted_to_workspace_id,
			       amount_bdt_subunits, COALESCE(reason_note,''), ledger_entry_id, currency, created_at
			FROM public.credit_grants
			ORDER BY created_at DESC, id DESC
			LIMIT $1`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("grants: list query: %w", err)
	}
	defer rows.Close()

	var out []CreditGrant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("grants: iterate rows: %w", err)
	}
	return out, nil
}

// =============================================================================
// Internal helpers
// =============================================================================

type scanner interface {
	Scan(dest ...any) error
}

func scanGrant(s scanner) (CreditGrant, error) {
	var g CreditGrant
	var amount int64
	if err := s.Scan(
		&g.ID,
		&g.GrantedByUserID,
		&g.GrantedToUserID,
		&g.GrantedToWorkspaceID,
		&amount,
		&g.ReasonNote,
		&g.LedgerEntryID,
		&g.Currency,
		&g.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return CreditGrant{}, ErrNotFound
		}
		return CreditGrant{}, fmt.Errorf("grants: scan: %w", err)
	}
	g.AmountBDTSubunits = new(big.Int).SetInt64(amount)
	return g, nil
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
