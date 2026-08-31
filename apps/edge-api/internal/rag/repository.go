package rag

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
)

// DocRow mirrors rag_documents columns needed by the edge handler.
type DocRow struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	MimeType  string
	SizeBytes int64
	Status    string
	CreatedAt time.Time
}

// ChunkRow is a search result from rag_chunks.
type ChunkRow struct {
	ID         uuid.UUID
	DocumentID uuid.UUID
	// DocumentName is the source document's name, joined from
	// public.rag_documents so a citation can name its source without a second
	// query per chunk (issue #1595).
	DocumentName string
	Content      string
	Score        float32
}

// Repo handles rag DB operations in the edge-api.
// RLS is enforced by setting app.current_tenant_id before every query.
type Repo struct {
	pool *pgxpool.Pool
	// pgType is the active rag_chunks.embedding pgvector column type
	// ("vector" or "halfvec"), from the resolved embedding Plan. It selects
	// the query-vector cast in SearchChunks so the cast matches the column
	// type, keeping the HNSW index usable and avoiding a per-row cast that
	// would force a sequential scan (a mismatched cast degrades, it does not
	// error).
	pgType string
}

// NewRepo creates a Repo backed by pool. pgType is the provisioned pgvector
// column type ("vector"/"halfvec"); an empty value defaults to "vector" (the
// shipped column type) via embedmodel.Cast.
func NewRepo(pool *pgxpool.Pool, pgType string) *Repo {
	return &Repo{pool: pool, pgType: pgType}
}

// withTenantTx runs fn inside an explicit transaction with the RLS session
// variable set LOCAL (transaction-scoped) to tenantID. hive_app is NOT
// BYPASSRLS (20260518_04_phase19_audit_rls_and_indexes.sql), so every query
// against public.rag_documents / public.rag_chunks must see
// app.current_tenant_id set to the caller's tenant.
//
// A bare conn.Exec(set_config(..., true)) followed by a separate
// conn.QueryRow/Exec with no transaction does not work: LOCAL resets the
// instant the Exec's own implicit (autocommit) transaction ends, so the
// following query sees current_setting() back at NULL and the RLS policy
// denies everything (reads return zero rows, writes fail WITH CHECK).
// Wrapping in Begin/Commit makes LOCAL correct: it applies for exactly this
// transaction's statements and is guaranteed to clear at Commit or Rollback,
// so nothing survives onto the pooled connection for the next borrower.
// Mirrors apps/control-plane/internal/rag/repository.go.
func (r *Repo) withTenantTx(ctx context.Context, tenantID uuid.UUID, fn func(tx pgx.Tx) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rag.repo: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op once Commit has succeeded

	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("rag.repo: set tenant: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// InsertDocument registers a new rag_document row (status=pending) and
// returns its assigned id.
func (r *Repo) InsertDocument(ctx context.Context, tenantID uuid.UUID, name, mimeType string, sizeBytes int64) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO public.rag_documents (tenant_id, name, mime_type, size_bytes, status)
			VALUES ($1, $2, $3, $4, 'pending')
			RETURNING id`,
			tenantID, name, mimeType, sizeBytes,
		).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: insert document: %w", err)
	}
	return id, nil
}

// GetDocument fetches one document by id scoped to tenantID.
func (r *Repo) GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (DocRow, error) {
	var d DocRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, tenant_id, name, mime_type, size_bytes, status, created_at
			FROM public.rag_documents WHERE id = $1`,
			docID,
		).Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes, &d.Status, &d.CreatedAt)
	})
	if err != nil {
		return DocRow{}, fmt.Errorf("rag.repo: get document: %w", err)
	}
	return d, nil
}

// ListDocuments returns all documents for a tenant, newest first.
func (r *Repo) ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]DocRow, error) {
	var docs []DocRow
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, name, mime_type, size_bytes, status, created_at
			FROM public.rag_documents ORDER BY created_at DESC`)
		if err != nil {
			return fmt.Errorf("rag.repo: list: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var d DocRow
			if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType,
				&d.SizeBytes, &d.Status, &d.CreatedAt); err != nil {
				return fmt.Errorf("rag.repo: scan: %w", err)
			}
			docs = append(docs, d)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return docs, nil
}

// DeleteDocument deletes a document (chunks cascade via FK).
// Returns found=true when a row was actually removed, false when no row matched.
func (r *Repo) DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) (bool, error) {
	var found bool
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM public.rag_documents WHERE id = $1 AND tenant_id = $2`, docID, tenantID)
		if err != nil {
			return fmt.Errorf("rag.repo: delete: %w", err)
		}
		found = tag.RowsAffected() > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return found, nil
}

// EmbeddingMismatch reports whether tenantID has any embedded document whose
// stored provenance (embedding_model, embedding_dim) differs from the
// currently configured model + dim. A true result means at least one of the
// tenant's documents was embedded under a different model/dimension than
// this process is configured for right now -- comparing today's query vector
// against those chunks would silently mix two incompatible vector spaces.
// The handler uses this to fail RAG search closed instead (WithEmbeddingGuard);
// this package does not re-embed anything (PR2).
//
// model is canonicalized before the comparison, the same way the control-plane
// ingest and re-embed paths canonicalize before stamping provenance. Without it
// a process configured with the LiteLLM route alias compared alias against the
// canonical id every stored row holds and failed every request closed for a
// corpus that was in fact embedded under exactly this model.
func (r *Repo) EmbeddingMismatch(ctx context.Context, tenantID uuid.UUID, model string, dim int) (bool, error) {
	var mismatch bool
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM public.rag_documents
				WHERE status = 'embedded' AND (embedding_model != $1 OR embedding_dim != $2)
			)`,
			embedmodel.Canonical(model), dim,
		).Scan(&mismatch)
	})
	if err != nil {
		return false, fmt.Errorf("rag.repo: embedding mismatch check: %w", err)
	}
	return mismatch, nil
}

// encodeVector serialises []float32 to pgvector text format '[v1,v2,...]'.
// Returns an error if any value is NaN or Inf — pgvector rejects those and
// inserting them would silently corrupt ANN results.
func encodeVector(v []float32) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	sb := strings.Builder{}
	sb.WriteByte('[')
	for i, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return "", fmt.Errorf("rag: vector[%d] is not finite (%v)", i, f)
		}
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", f))
	}
	sb.WriteByte(']')
	return sb.String(), nil
}

// SearchChunks performs cosine vector similarity search scoped to the tenant,
// and optionally narrowed to one project. queryVec must be EmbeddingDimension
// floats. Results are ordered most similar first.
//
// projectID is uuid.Nil for "the tenant's whole corpus", which is what every
// caller did before Projects existed (issue #1595). A non-Nil value narrows the
// search to documents attached to that project, and it MUST already have been
// authorized by the caller: this method filters, it does not decide who is
// allowed to filter by what. Handler.authorizeProject is the decision, and it
// runs before this call rather than inside it, so a second caller cannot reach
// the filter without also reaching the check.
func (r *Repo) SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int, projectID uuid.UUID) ([]ChunkRow, error) {
	if topK <= 0 {
		topK = 5
	}

	vec, err := encodeVector(queryVec)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: encode vector: %w", err)
	}

	// An absent project scope travels as a SQL NULL so one statement serves
	// both shapes rather than two near-identical queries drifting apart.
	var project any
	if projectID != uuid.Nil {
		project = projectID
	}

	var results []ChunkRow
	err = r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		// Explicit tenant_id filter is defense-in-depth alongside RLS:
		// protects against SECURITY DEFINER / superuser-bypass scenarios.
		rows, err := tx.Query(ctx, searchChunksQuery(r.pgType), vec, topK, tenantID, project)
		if err != nil {
			return fmt.Errorf("rag.repo: search: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var c ChunkRow
			serr := rows.Scan(&c.ID, &c.DocumentID, &c.DocumentName, &c.Content, &c.Score)
			if serr != nil {
				return fmt.Errorf("rag.repo: scan: %w", serr)
			}
			results = append(results, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// searchChunksQuery builds the tenant-scoped cosine-search SQL with the
// query-vector cast matched to the active embedding column type. The cast
// suffix comes from embedmodel.Cast (an enum-constrained "::vector"/"::halfvec",
// never user input), so interpolating it is injection-safe; the query vector,
// the tenant and the project scope all remain bound parameters. Pure and
// side-effect free so the cast selection is unit-testable without a live
// database.
//
// $4 is the optional project scope: NULL means the whole tenant corpus, so one
// statement serves both shapes.
//
// ponytail: the project filter is applied after the HNSW ordering, so a
// project-scoped search over a large tenant corpus can return fewer than topK
// passages even when the project holds more. That is pgvector's ordinary
// post-filter behaviour and it is fine at the corpus sizes this ships for. The
// upgrade, if a tenant ever outgrows it, is a partial index per project or an
// iterative scan, not a second query path.
func searchChunksQuery(pgType string) string {
	cast := embedmodel.Cast(pgType)
	return fmt.Sprintf(`
				SELECT c.id, c.document_id, d.name, c.content,
				       (c.embedding <=> $1%[1]s)::float4 AS score
				FROM public.rag_chunks c
				JOIN public.rag_documents d
				  ON d.id = c.document_id AND d.tenant_id = c.tenant_id
				WHERE c.tenant_id = $3
				  AND ($4::uuid IS NULL OR d.project_id = $4::uuid)
				ORDER BY c.embedding <=> $1%[1]s
				LIMIT $2`, cast)
}
