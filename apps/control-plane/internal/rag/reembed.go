package rag

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sakibsadmanshajib/hive/packages/embedmodel"
)

// embedInBatches embeds texts in fixed-size batches through embed, preserving
// input order. A single batch error fails the whole call (fail-closed): the
// caller must not persist a partial vector set that would leave a document
// half-migrated across two embedding spaces. batchSize <= 0 defaults to 32.
func embedInBatches(ctx context.Context, embed EmbedClient, texts []string, batchSize int) ([][]float32, error) {
	if batchSize <= 0 {
		batchSize = 32
	}
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := embed.Embed(ctx, texts[start:end])
		if err != nil {
			return nil, fmt.Errorf("rag.reembed: embed batch [%d:%d]: %w", start, end, err)
		}
		if len(vecs) != end-start {
			return nil, fmt.Errorf("rag.reembed: embed batch [%d:%d] returned %d vectors, want %d", start, end, len(vecs), end-start)
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// Reembedder rewrites the vectors of every document whose stored provenance
// does not match the active (model, dim) onto that active configuration. It
// runs on a privileged (RLS-bypassing owner) connection so it can walk every
// tenant's corpus after a model/dimension switch.
//
// Each document is re-embedded in a single transaction (all its chunks plus
// its provenance row), so a document is either fully migrated or left pending;
// a partial document is never committed. A document already at the active
// configuration is skipped, so RunOnce is idempotent and resumable after a
// restart. A per-document failure leaves that document pending (the per-tenant
// guard keeps its tenant fail-closed) and does not abort the whole run.
type Reembedder struct {
	pool      *pgxpool.Pool
	embed     EmbedClient
	batchSize int
	model     string // canonical active model recorded as provenance
	dim       int
}

// NewReembedder constructs a Reembedder. model should be the active
// EMBEDDING_MODEL (stored as provenance) and dim the active EMBEDDING_DIM.
// batchSize 0 defaults to 32.
func NewReembedder(pool *pgxpool.Pool, embed EmbedClient, batchSize int, model string, dim int) *Reembedder {
	if batchSize <= 0 {
		batchSize = 32
	}
	return &Reembedder{pool: pool, embed: embed, batchSize: batchSize, model: model, dim: dim}
}

// pendingDocIDs returns the ids of documents not yet at the active (model,
// dim), across all tenants. "Done" means status='embedded' with matching
// provenance; everything else (pending, error, or a stale model/dim) is a
// candidate. Model comparison is canonical so a route alias and its canonical
// id are treated as one model.
func (rb *Reembedder) pendingDocIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := rb.pool.Query(ctx, `
		SELECT id FROM public.rag_documents
		WHERE NOT (status = 'embedded' AND embedding_model = $1 AND embedding_dim = $2)
		ORDER BY created_at ASC`,
		embedmodel.Canonical(rb.model), rb.dim)
	if err != nil {
		return nil, fmt.Errorf("rag.reembed: list pending: %w", err)
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("rag.reembed: scan id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// RunOnce re-embeds every currently pending document and returns how many were
// migrated in this pass and how many remain pending afterward (nonzero when
// some documents failed and were left fail-closed). It is safe to call
// repeatedly; each call skips documents already at the active configuration.
func (rb *Reembedder) RunOnce(ctx context.Context) (done int, remaining int, err error) {
	ids, err := rb.pendingDocIDs(ctx)
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if rerr := rb.reembedDocument(ctx, id); rerr != nil {
			// Leave the document pending (guard stays closed) and continue;
			// the next RunOnce retries it.
			remaining++
			continue
		}
		done++
	}
	return done, remaining, nil
}

// reembedDocument re-embeds all chunk text of one document and stamps its
// provenance, atomically. The canonical model is recorded so a later run
// recognizes the document as done.
func (rb *Reembedder) reembedDocument(ctx context.Context, docID uuid.UUID) error {
	tx, err := rb.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rag.reembed: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, content FROM public.rag_chunks
		WHERE document_id = $1 ORDER BY chunk_index ASC`, docID)
	if err != nil {
		return fmt.Errorf("rag.reembed: load chunks: %w", err)
	}
	var chunkIDs []uuid.UUID
	var texts []string
	for rows.Next() {
		var id uuid.UUID
		var content string
		if err := rows.Scan(&id, &content); err != nil {
			rows.Close()
			return fmt.Errorf("rag.reembed: scan chunk: %w", err)
		}
		chunkIDs = append(chunkIDs, id)
		texts = append(texts, content)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rag.reembed: iterate chunks: %w", err)
	}

	if len(chunkIDs) > 0 {
		vecs, err := embedInBatches(ctx, rb.embed, texts, rb.batchSize)
		if err != nil {
			return err // fail-closed: nothing persisted
		}
		for i, cid := range chunkIDs {
			enc, err := encodeVector(vecs[i])
			if err != nil {
				return fmt.Errorf("rag.reembed: encode chunk %d: %w", i, err)
			}
			if _, err := tx.Exec(ctx,
				`UPDATE public.rag_chunks SET embedding = $1::vector WHERE id = $2`,
				enc, cid); err != nil {
				return fmt.Errorf("rag.reembed: update chunk: %w", err)
			}
		}
	}

	if _, err := tx.Exec(ctx, `
		UPDATE public.rag_documents
		SET status = 'embedded', error_msg = NULL,
		    embedding_model = $1, embedding_dim = $2, updated_at = now()
		WHERE id = $3`,
		embedmodel.Canonical(rb.model), rb.dim, docID); err != nil {
		return fmt.Errorf("rag.reembed: set provenance: %w", err)
	}
	return tx.Commit(ctx)
}
