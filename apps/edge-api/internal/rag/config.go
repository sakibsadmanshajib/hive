package rag

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LoadActiveEmbeddingConfig reads the singleton public.rag_embedding_config
// row (id = 1): the model + dim the live rag_chunks.embedding column was
// provisioned to. control-plane owns writes to this row (it runs provisioning);
// edge-api reads it at startup and reconciles its own resolved EMBEDDING_MODEL
// / EMBEDDING_DIM against it (embedmodel.SameConfig), disabling RAG on a
// mismatch instead of querying a column built for a different embedding space
// (#368).
//
// found is false when the table has no row yet (fresh schema before the seed
// migration), which the caller treats as "nothing to reconcile against."
func LoadActiveEmbeddingConfig(ctx context.Context, pool *pgxpool.Pool) (model string, dim int, found bool, err error) {
	err = pool.QueryRow(ctx, `
		SELECT model, dim FROM public.rag_embedding_config WHERE id = 1`).Scan(&model, &dim)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, fmt.Errorf("rag: load active embedding config: %w", err)
	}
	return model, dim, true, nil
}
