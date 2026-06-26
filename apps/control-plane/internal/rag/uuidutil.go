package rag

import (
	"fmt"

	"github.com/google/uuid"
)

// mustParseUUID parses a UUID string and panics on invalid input.
// Used only in Ingest where the caller is expected to pass valid uuid.UUID.String() values.
func mustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("rag: invalid uuid %q: %v", s, err))
	}
	return id
}
