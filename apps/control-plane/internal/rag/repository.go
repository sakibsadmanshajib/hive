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

// DocumentStatus values mirror the rag_documents.status CHECK constraint.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusEmbedded   = "embedded"
	StatusError      = "error"
)

// Document is a row from rag_documents.
type Document struct {
	ID          uuid.UUID
	TenantID    uuid.UUID
	Name        string
	MimeType    string
	SizeBytes   int64
	Status      string
	ErrorMsg    string
	StoragePath string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ChunkRow is a row from rag_chunks (no embedding in list queries).
type ChunkRow struct {
	ID         uuid.UUID
	TenantID   uuid.UUID
	DocumentID uuid.UUID
	ChunkIndex int
	Content    string
	TokenCount int
	Score      float32 // populated by search queries only
}

// Repo handles all rag_documents / rag_chunks database operations.
// Every method sets the RLS session variable before executing so the
// Postgres policy enforces tenant isolation automatically.
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

// NewRepo creates a Repo backed by the given pool. pgType is the provisioned
// pgvector column type ("vector"/"halfvec"); an empty value defaults to
// "vector" (the shipped column type) via embedmodel.Cast.
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
// Mirrors apps/control-plane/internal/egress/repository.go.
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

// InsertDocument creates a new rag_document row and returns the assigned id.
func (r *Repo) InsertDocument(ctx context.Context, d Document) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.withTenantTx(ctx, d.TenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO public.rag_documents
			    (tenant_id, name, mime_type, size_bytes, status, storage_path)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id`,
			d.TenantID, d.Name, d.MimeType, d.SizeBytes, StatusPending, d.StoragePath,
		).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: insert document: %w", err)
	}
	return id, nil
}

// GetDocument fetches a single document by id, scoped to tenantID via RLS.
func (r *Repo) GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (Document, error) {
	var d Document
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, tenant_id, name, mime_type, size_bytes, status,
			       COALESCE(error_msg,''), COALESCE(storage_path,''),
			       created_at, updated_at
			FROM public.rag_documents
			WHERE id = $1`,
			docID,
		).Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes,
			&d.Status, &d.ErrorMsg, &d.StoragePath, &d.CreatedAt, &d.UpdatedAt)
	})
	if err != nil {
		return Document{}, fmt.Errorf("rag.repo: get document: %w", err)
	}
	return d, nil
}

// ListDocuments returns all documents for a tenant, newest first.
func (r *Repo) ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]Document, error) {
	var docs []Document
	err := r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, name, mime_type, size_bytes, status,
			       COALESCE(error_msg,''), COALESCE(storage_path,''),
			       created_at, updated_at
			FROM public.rag_documents
			ORDER BY created_at DESC`)
		if err != nil {
			return fmt.Errorf("rag.repo: list documents: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var d Document
			if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes,
				&d.Status, &d.ErrorMsg, &d.StoragePath, &d.CreatedAt, &d.UpdatedAt); err != nil {
				return fmt.Errorf("rag.repo: scan document: %w", err)
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

// UpdateDocumentStatus updates the status (and optionally error_msg) of a document.
func (r *Repo) UpdateDocumentStatus(ctx context.Context, tenantID, docID uuid.UUID, status, errMsg string) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE public.rag_documents
			SET status = $1, error_msg = NULLIF($2,''), updated_at = now()
			WHERE id = $3`,
			status, errMsg, docID,
		)
		if err != nil {
			return fmt.Errorf("rag.repo: update document status: %w", err)
		}
		return nil
	})
}

// SetEmbeddedProvenance marks a document embedded and records the model +
// dimension that produced its vectors, in one statement. Ingester calls this
// instead of UpdateDocumentStatus(..., StatusEmbedded, "") so provenance is
// never left at the column DEFAULT for documents actually embedded under a
// different, later configuration (needed to find stale rows once a re-embed
// job exists — PR2, not built here).
func (r *Repo) SetEmbeddedProvenance(ctx context.Context, tenantID, docID uuid.UUID, model string, dim int) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE public.rag_documents
			SET status = $1, error_msg = NULL, embedding_model = $2, embedding_dim = $3, updated_at = now()
			WHERE id = $4`,
			StatusEmbedded, model, dim, docID,
		)
		if err != nil {
			return fmt.Errorf("rag.repo: set embedded provenance: %w", err)
		}
		return nil
	})
}

// DeleteDocument deletes a document and cascades to its chunks.
func (r *Repo) DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) error {
	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`DELETE FROM public.rag_documents WHERE id = $1`,
			docID,
		)
		if err != nil {
			return fmt.Errorf("rag.repo: delete document: %w", err)
		}
		return nil
	})
}

// InsertChunks bulk-inserts chunk rows. Embeddings are stored via pgvector
// text-literal cast: '[0.1,0.2,...]'::vector — no extra library required.
func (r *Repo) InsertChunks(ctx context.Context, tenantID, docID uuid.UUID, chunks []Chunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("rag.repo: chunks/embeddings length mismatch: %d vs %d", len(chunks), len(embeddings))
	}

	return r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		for i, ch := range chunks {
			vec, err := encodeVector(embeddings[i])
			if err != nil {
				return fmt.Errorf("rag.repo: encode chunk %d vector: %w", i, err)
			}
			_, err = tx.Exec(ctx, `
				INSERT INTO public.rag_chunks
				    (tenant_id, document_id, chunk_index, content, token_count, embedding)
				VALUES ($1, $2, $3, $4, $5, $6::vector)`,
				tenantID, docID, ch.Index, ch.Content, ch.TokenCount, vec,
			)
			if err != nil {
				return fmt.Errorf("rag.repo: insert chunk %d: %w", i, err)
			}
		}
		return nil
	})
}

// SearchChunks runs a cosine-distance vector search returning up to topK
// chunk rows scoped to the caller's tenant. Results are ordered by
// ascending distance (most similar first).
//
// The query vector is passed as a parameterised text literal to avoid
// any string interpolation of floating-point values.
func (r *Repo) SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int) ([]ChunkRow, error) {
	if topK <= 0 {
		topK = 5
	}

	vec, err := encodeVector(queryVec)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: encode query vector: %w", err)
	}

	var results []ChunkRow
	err = r.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		// Explicit tenant_id filter is defense-in-depth alongside RLS:
		// protects against SECURITY DEFINER / superuser-bypass scenarios.
		rows, err := tx.Query(ctx, searchChunksQuery(r.pgType), vec, topK, tenantID)
		if err != nil {
			return fmt.Errorf("rag.repo: search: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var c ChunkRow
			if err := rows.Scan(&c.ID, &c.TenantID, &c.DocumentID,
				&c.ChunkIndex, &c.Content, &c.TokenCount, &c.Score); err != nil {
				return fmt.Errorf("rag.repo: scan chunk: %w", err)
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
// never user input), so interpolating it is injection-safe; the query vector
// and all values remain bound parameters. Pure and side-effect free so the
// cast selection is unit-testable without a live database.
func searchChunksQuery(pgType string) string {
	cast := embedmodel.Cast(pgType)
	return fmt.Sprintf(`
			SELECT id, tenant_id, document_id, chunk_index, content, token_count,
			       (embedding <=> $1%[1]s)::float4 AS score
			FROM public.rag_chunks
			WHERE tenant_id = $3
			ORDER BY embedding <=> $1%[1]s
			LIMIT $2`, cast)
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
