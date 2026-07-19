package rag

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
)

// hnswIndexName is the single ANN index recreated on rag_chunks.embedding.
// It is dropped and rebuilt to match the active (type, opclass); the name is
// stable so idempotency and the base migration agree.
const hnswIndexName = "rag_chunks_embedding_hnsw_idx"

// provisionAdvisoryLockKey is a fixed, arbitrary key for the transaction-level
// advisory lock that serializes Provision. Two concurrent provisions (e.g. two
// control-plane replicas restarting at once) would otherwise interleave the
// DROP INDEX / DROP COLUMN / ADD COLUMN / CREATE INDEX sequence and corrupt the
// schema. pg_advisory_xact_lock blocks the second caller until the first
// commits or rolls back, and auto-releases with the transaction.
const provisionAdvisoryLockKey int64 = 0x726167656d626564 // "ragembed"

// ActiveConfig is the singleton public.rag_embedding_config row: the active,
// provisioned embedding configuration both services reconcile against.
type ActiveConfig struct {
	Model     string
	Dim       int
	PgType    string
	Opclass   string
	Indexable bool
}

// LoadActiveConfig reads the singleton rag_embedding_config row (id = 1).
// found is false when the table has no row yet (pre-seed / fresh schema),
// which callers treat as "nothing to reconcile against," not an error.
func LoadActiveConfig(ctx context.Context, pool *pgxpool.Pool) (cfg ActiveConfig, found bool, err error) {
	row := pool.QueryRow(ctx, `
		SELECT model, dim, pgvector_type, opclass, indexable
		FROM public.rag_embedding_config WHERE id = 1`)
	err = row.Scan(&cfg.Model, &cfg.Dim, &cfg.PgType, &cfg.Opclass, &cfg.Indexable)
	if errors.Is(err, pgx.ErrNoRows) {
		return ActiveConfig{}, false, nil
	}
	if err != nil {
		return ActiveConfig{}, false, fmt.Errorf("rag.provision: load active config: %w", err)
	}
	return cfg, true, nil
}

// currentEmbeddingColumnType returns the SQL type text of
// public.rag_chunks.embedding, e.g. "vector(1024)" or "halfvec(3000)". found
// is false when the column has been dropped (mid-provision) or never existed.
func currentEmbeddingColumnType(ctx context.Context, tx pgx.Tx) (typeText string, found bool, err error) {
	err = tx.QueryRow(ctx, `
		SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c     ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public'
		  AND c.relname = 'rag_chunks'
		  AND a.attname = 'embedding'
		  AND NOT a.attisdropped`).Scan(&typeText)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("rag.provision: read column type: %w", err)
	}
	return typeText, true, nil
}

// indexExists reports whether the HNSW index on rag_chunks.embedding is
// present, so an already-typed-but-unindexed column is still (re)indexed.
func indexExists(ctx context.Context, tx pgx.Tx) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM pg_indexes
			WHERE schemaname = 'public' AND tablename = 'rag_chunks' AND indexname = $1)`,
		hnswIndexName).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("rag.provision: check index: %w", err)
	}
	return exists, nil
}

// Provision makes public.rag_chunks.embedding and its ANN index match plan,
// idempotently, then records the choice in rag_embedding_config. It must run
// on a privileged (DDL-capable) connection, not the hive_app app role.
//
// Idempotency: if the column is already plan.ColumnType() AND the index state
// already matches plan.Indexable, Provision only refreshes the config row and
// returns without touching data. Otherwise it drops the index and column,
// recreates the column at the target type (nullable, so the re-embed worker
// can repopulate it incrementally), rebuilds the index for the resolved
// opclass, re-asserts grants + RLS, and marks every previously embedded
// document pending so the per-tenant guard fails closed until the re-embed
// worker rewrites its vectors onto the new space.
//
// pgvector cannot ALTER a vector column's dimension in place, and two vector
// spaces must never coexist in one ANN index, so a switch is deliberately a
// destroy-and-re-embed, never a live cutover. Chunk text rows are preserved;
// only the embedding column and its provenance are rewritten.
func Provision(ctx context.Context, pool *pgxpool.Pool, plan embedmodel.Plan) error {
	if plan.Dim <= 0 || plan.PgType == "" {
		return fmt.Errorf("rag.provision: refusing to provision an unresolved plan (%+v)", plan)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rag.provision: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent provisions: hold a transaction-scoped advisory lock
	// before the destructive DROP/CREATE sequence so two replicas cannot
	// interleave and leave rag_chunks.embedding half-rebuilt. Released on
	// commit/rollback.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, provisionAdvisoryLockKey); err != nil {
		return fmt.Errorf("rag.provision: acquire advisory lock: %w", err)
	}

	curType, colFound, err := currentEmbeddingColumnType(ctx, tx)
	if err != nil {
		return err
	}
	idxPresent, err := indexExists(ctx, tx)
	if err != nil {
		return err
	}

	target := plan.ColumnType()
	alreadyProvisioned := colFound && curType == target && idxPresent == plan.Indexable
	if !alreadyProvisioned {
		if err := recreateEmbeddingColumn(ctx, tx, plan); err != nil {
			return err
		}
		// Fail-closed: previously embedded documents live in the old vector
		// space and must not be queried against the new column. Mark them
		// pending; the per-tenant guard blocks those tenants until re-embed.
		if _, err := tx.Exec(ctx, `
			UPDATE public.rag_documents
			SET status = 'pending', updated_at = now()
			WHERE status IN ('embedded','processing')`); err != nil {
			return fmt.Errorf("rag.provision: mark documents pending: %w", err)
		}
	}

	if err := upsertActiveConfig(ctx, tx, plan); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rag.provision: commit: %w", err)
	}
	return nil
}

// recreateEmbeddingColumn drops the index + embedding column and rebuilds them
// to plan's (type, opclass). Column identifiers are never interpolated from
// user input: pgType/opclass come only from embedmodel.ResolvePgvector and dim
// is an int, so the formatted DDL cannot carry injection.
func recreateEmbeddingColumn(ctx context.Context, tx pgx.Tx, plan embedmodel.Plan) error {
	if _, err := tx.Exec(ctx, fmt.Sprintf(`DROP INDEX IF EXISTS public.%s`, hnswIndexName)); err != nil {
		return fmt.Errorf("rag.provision: drop index: %w", err)
	}
	if _, err := tx.Exec(ctx, `ALTER TABLE public.rag_chunks DROP COLUMN IF EXISTS embedding`); err != nil {
		return fmt.Errorf("rag.provision: drop column: %w", err)
	}
	// Nullable on purpose: existing chunk rows keep their content and get their
	// vectors written by the re-embed worker; a NOT NULL add would fail on a
	// non-empty table and forbid incremental repopulation.
	if _, err := tx.Exec(ctx, fmt.Sprintf(
		`ALTER TABLE public.rag_chunks ADD COLUMN embedding %s`, plan.ColumnType())); err != nil {
		return fmt.Errorf("rag.provision: add column: %w", err)
	}

	if plan.Indexable {
		// vector -> hnsw (embedding vector_cosine_ops); halfvec -> the halfvec
		// opclass on the already-halfvec column. m/ef mirror the base migration.
		if _, err := tx.Exec(ctx, fmt.Sprintf(
			`CREATE INDEX %s ON public.rag_chunks USING hnsw (embedding %s) WITH (m = 16, ef_construction = 64)`,
			hnswIndexName, plan.Opclass)); err != nil {
			return fmt.Errorf("rag.provision: create index: %w", err)
		}
	}

	// Re-assert privileges + tenant RLS on the recreated column. Table-level
	// FOR ALL policies survive a column drop, but re-asserting is cheap and
	// closes the schema-drift / RLS-gap risk the runtime-DDL path carries.
	if _, err := tx.Exec(ctx, `GRANT SELECT, INSERT, UPDATE, DELETE ON public.rag_chunks TO hive_app`); err != nil {
		return fmt.Errorf("rag.provision: re-grant hive_app: %w", err)
	}
	if _, err := tx.Exec(ctx, `GRANT SELECT ON public.rag_chunks TO auditor_ro`); err != nil {
		return fmt.Errorf("rag.provision: re-grant auditor_ro: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_policies
				WHERE schemaname = 'public' AND tablename = 'rag_chunks'
				  AND policyname = 'rag_chunks_tenant_isolation'
			) THEN
				CREATE POLICY rag_chunks_tenant_isolation
					ON public.rag_chunks AS PERMISSIVE FOR ALL TO hive_app
					USING      (tenant_id = current_setting('app.current_tenant_id', true)::uuid)
					WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true)::uuid);
			END IF;
		END$$;`); err != nil {
		return fmt.Errorf("rag.provision: re-assert rls: %w", err)
	}
	return nil
}

// upsertActiveConfig writes plan into the singleton rag_embedding_config row.
func upsertActiveConfig(ctx context.Context, tx pgx.Tx, plan embedmodel.Plan) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO public.rag_embedding_config
			(id, model, dim, pgvector_type, opclass, indexable, provisioned_at, updated_at)
		VALUES (1, $1, $2, $3, $4, $5, now(), now())
		ON CONFLICT (id) DO UPDATE SET
			model = EXCLUDED.model,
			dim = EXCLUDED.dim,
			pgvector_type = EXCLUDED.pgvector_type,
			opclass = EXCLUDED.opclass,
			indexable = EXCLUDED.indexable,
			provisioned_at = now(),
			updated_at = now()`,
		plan.Model, plan.Dim, plan.PgType, plan.Opclass, plan.Indexable)
	if err != nil {
		return fmt.Errorf("rag.provision: upsert active config: %w", err)
	}
	return nil
}
