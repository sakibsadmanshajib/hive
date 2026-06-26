package rag

import (
	"fmt"

	"github.com/google/uuid"
)

// parseUUID parses a UUID string, returning an error for invalid input.
// The async ingest path must not panic on bad input — callers propagate the error.
func parseUUID(s string) (uuid.UUID, error) {
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, fmt.Errorf("rag: invalid uuid %q: %w", s, err)
	}
	return id, nil
}
