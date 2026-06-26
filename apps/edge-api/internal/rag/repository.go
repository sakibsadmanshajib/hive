package rag

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	Content    string
	Score      float32
}

// Repo handles rag DB operations in the edge-api.
// RLS is enforced by setting app.current_tenant_id before every query.
type Repo struct {
	pool *pgxpool.Pool
}

// NewRepo creates a Repo backed by pool.
func NewRepo(pool *pgxpool.Pool) *Repo { return &Repo{pool: pool} }

// InsertDocument registers a new rag_document row (status=pending) and
// returns its assigned id.
func (r *Repo) InsertDocument(ctx context.Context, tenantID uuid.UUID, name, mimeType string, sizeBytes int64) (uuid.UUID, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_tenant_id', $1, true)",
		tenantID.String()); err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	var id uuid.UUID
	err = conn.QueryRow(ctx, `
		INSERT INTO public.rag_documents (tenant_id, name, mime_type, size_bytes, status)
		VALUES ($1, $2, $3, $4, 'pending')
		RETURNING id`,
		tenantID, name, mimeType, sizeBytes,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag.repo: insert document: %w", err)
	}
	return id, nil
}

// GetDocument fetches one document by id scoped to tenantID.
func (r *Repo) GetDocument(ctx context.Context, tenantID, docID uuid.UUID) (DocRow, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return DocRow{}, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_tenant_id', $1, true)",
		tenantID.String()); err != nil {
		return DocRow{}, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	var d DocRow
	err = conn.QueryRow(ctx, `
		SELECT id, tenant_id, name, mime_type, size_bytes, status, created_at
		FROM public.rag_documents WHERE id = $1`,
		docID,
	).Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType, &d.SizeBytes, &d.Status, &d.CreatedAt)
	if err != nil {
		return DocRow{}, fmt.Errorf("rag.repo: get document: %w", err)
	}
	return d, nil
}

// ListDocuments returns all documents for a tenant, newest first.
func (r *Repo) ListDocuments(ctx context.Context, tenantID uuid.UUID) ([]DocRow, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_tenant_id', $1, true)",
		tenantID.String()); err != nil {
		return nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	rows, err := conn.Query(ctx, `
		SELECT id, tenant_id, name, mime_type, size_bytes, status, created_at
		FROM public.rag_documents ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: list: %w", err)
	}
	defer rows.Close()

	var docs []DocRow
	for rows.Next() {
		var d DocRow
		if err := rows.Scan(&d.ID, &d.TenantID, &d.Name, &d.MimeType,
			&d.SizeBytes, &d.Status, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("rag.repo: scan: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// DeleteDocument deletes a document (chunks cascade via FK).
// Returns found=true when a row was actually removed, false when no row matched.
func (r *Repo) DeleteDocument(ctx context.Context, tenantID, docID uuid.UUID) (bool, error) {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return false, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_tenant_id', $1, true)",
		tenantID.String()); err != nil {
		return false, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	tag, err := conn.Exec(ctx, `DELETE FROM public.rag_documents WHERE id = $1 AND tenant_id = $2`, docID, tenantID)
	if err != nil {
		return false, fmt.Errorf("rag.repo: delete: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SearchChunks performs cosine vector similarity search scoped to the tenant.
// queryVec must be EmbeddingDimension floats. Results are ordered most similar first.
func (r *Repo) SearchChunks(ctx context.Context, tenantID uuid.UUID, queryVec []float32, topK int) ([]ChunkRow, error) {
	if topK <= 0 {
		topK = 5
	}

	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: acquire: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx,
		"SELECT set_config('app.current_tenant_id', $1, true)",
		tenantID.String()); err != nil {
		return nil, fmt.Errorf("rag.repo: set tenant: %w", err)
	}

	vec, err := encodeVector(queryVec)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: encode vector: %w", err)
	}
	// Explicit tenant_id filter is defense-in-depth alongside RLS:
	// protects against SECURITY DEFINER / superuser-bypass scenarios.
	rows, err := conn.Query(ctx, `
		SELECT id, document_id, content,
		       (embedding <=> $1::vector)::float4 AS score
		FROM public.rag_chunks
		WHERE tenant_id = $3
		ORDER BY embedding <=> $1::vector
		LIMIT $2`,
		vec, topK, tenantID,
	)
	if err != nil {
		return nil, fmt.Errorf("rag.repo: search: %w", err)
	}
	defer rows.Close()

	var results []ChunkRow
	for rows.Next() {
		var c ChunkRow
		if err := rows.Scan(&c.ID, &c.DocumentID, &c.Content, &c.Score); err != nil {
			return nil, fmt.Errorf("rag.repo: scan: %w", err)
		}
		results = append(results, c)
	}
	return results, rows.Err()
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
