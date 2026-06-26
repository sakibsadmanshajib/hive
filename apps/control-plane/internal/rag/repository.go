package rag

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
}

// NewRepo creates a Repo backed by the given pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// InsertDocument creates a new rag_document row and returns the assigned id.
func (r *Repo) InsertDocument(ctx context.Context, d Document) (uuid.UUID, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", d.TenantID.String()); err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	var id uuid.UUID
	err = conn.QueryRow(ctx, `
		INSERT INTO public.rag_documents
		    (tenant_id, name, mime_type, size_bytes, status, storage_path)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		d.TenantID, d.Name, d.MimeType, d.SizeBytes, StatusPending, d.StoragePath,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: insert document: %w", err)
	}
	return id, nil
}

// GetDocument fetches a single document by id, scoped to tenantID via RLS.
func (r *Repo) GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (Document, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return Document{}, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return Document{}, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	var d Document
	err = conn.QueryRow(ctx, `
		SELECT id, tenant_id, name, mime_type, size_bytes, status,
		       COALESCE(error_msg,''), COALESCE(storage_path,''),
		       created_at, updated_at
		FROM public.rag_documents
		WHERE id = $1`,
		docID,
	).Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes,
		&d.Status, &d.ErrorMsg, &d.StoragePath, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return Document{}, fmt.Errorf("rag.repo: get document: %w", err)
	}
	return d, nil
}

// ListDocuments returns all documents for a tenant, newest first.
func (r *Repo) ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]Document, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	rows, err := conn.Query(ctx, `
		SELECT id, tenant_id, name, mime_type, size_bytes, status,
		       COALESCE(error_msg,''), COALESCE(storage_path,''),
		       created_at, updated_at
		FROM public.rag_documents
		ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: list documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes,
			&d.Status, &d.ErrorMsg, &d.StoragePath, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("rag.repo: scan document: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// UpdateDocumentStatus updates the status (and optionally error_msg) of a document.
func (r *Repo) UpdateDocumentStatus(ctx context.Context, tenantID, docID uuid.UUID, status, errMsg string) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	_, err = conn.Exec(ctx, `
		UPDATE public.rag_documents
		SET status = $1, error_msg = NULLIF($2,''), updated_at = now()
		WHERE id = $3`,
		status, errMsg, docID,
	)
	if err != nil {
		return fmt.Errorf("rag.repo: update document status: %w", err)
	}
	return nil
}

// DeleteDocument deletes a document and cascades to its chunks.
func (r *Repo) DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	_, err = conn.Exec(ctx,
		`DELETE FROM public.rag_documents WHERE id = $1`,
		docID,
	)
	if err != nil {
		return fmt.Errorf("rag.repo: delete document: %w", err)
	}
	return nil
}

// InsertChunks bulk-inserts chunk rows. Embeddings are stored via pgvector
// text-literal cast: '[0.1,0.2,...]'::vector — no extra library required.
func (r *Repo) InsertChunks(ctx context.Context, tenantID, docID uuid.UUID, chunks []Chunk, embeddings [][]float32) error {
	if len(chunks) != len(embeddings) {
		return fmt.Errorf("rag.repo: chunks/embeddings length mismatch: %d vs %d", len(chunks), len(embeddings))
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	for i, ch := range chunks {
		vec := encodeVector(embeddings[i])
		_, err := conn.Exec(ctx, `
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

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, "SELECT set_config('app.current_tenant_id', $1, true)", tenantID.String()); err != nil {
		return nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	vec := encodeVector(queryVec)
	rows, err := conn.Query(ctx, `
		SELECT id, tenant_id, document_id, chunk_index, content, token_count,
		       (embedding <=> $1::vector)::float4 AS score
		FROM public.rag_chunks
		ORDER BY embedding <=> $1::vector
		LIMIT $2`,
		vec, topK,
	)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: search: %w", err)
	}
	defer rows.Close()

	var results []ChunkRow
	for rows.Next() {
		var c ChunkRow
		if err := rows.Scan(&c.ID, &c.TenantID, &c.DocumentID,
			&c.ChunkIndex, &c.Content, &c.TokenCount, &c.Score); err != nil {
			return nil, fmt.Errorf("rag.repo: scan chunk: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
}

// encodeVector serialises []float32 to the pgvector text format: '[v1,v2,...]'.
// This lets us use the '::vector' cast in SQL without a third-party library.
func encodeVector(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	sb := strings.Builder{}
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%g", f))
	}
	sb.WriteByte(']')
	return sb.String()
}
