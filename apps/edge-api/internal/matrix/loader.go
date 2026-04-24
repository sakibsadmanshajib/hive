package matrix

import (
	"encoding/json"
	"fmt"
	"os"
)

// LoadMatrix reads a support-matrix.json file from disk and returns a SupportMatrix
// with the internal lookup map built.
func LoadMatrix(path string) (*SupportMatrix, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading support matrix: %w", err)
	}
	return LoadMatrixFromBytes(data)
}

// LoadMatrixFromBytes parses a support matrix from raw JSON bytes.
// This is useful for testing without file system access.
func LoadMatrixFromBytes(data []byte) (*SupportMatrix, error) {
	var m SupportMatrix
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing support matrix: %w", err)
	}
	m.buildLookup()
	return &m, nil
}
