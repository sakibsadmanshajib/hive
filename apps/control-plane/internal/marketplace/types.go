// Package marketplace owns the admin-curated MCP and skills marketplace
// (issue #309, agent-subsystem blueprint Step 2.3): one distribution
// mechanism covering MCP servers plus rules, skills, and prompt templates
// (Cursor Team Marketplace pattern), with a global catalog an admin curates
// and a per-tenant enablement layer. The user-added, desktop-only tier is
// Wave 4, out of scope here.
package marketplace

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Kind identifies what a catalog entry distributes. Every kind shares the
// same catalog table and admin CRUD surface; only the shape of Config
// differs by convention (kind-specific, not enforced by a Postgres schema).
type Kind string

const (
	KindMCPServer      Kind = "mcp_server"
	KindRule           Kind = "rule"
	KindSkill          Kind = "skill"
	KindPromptTemplate Kind = "prompt_template"
)

// Valid reports whether k is one of the catalog kinds
// public.marketplace_entries' CHECK constraint accepts.
func (k Kind) Valid() bool {
	switch k {
	case KindMCPServer, KindRule, KindSkill, KindPromptTemplate:
		return true
	default:
		return false
	}
}

// Entry is one row of the global, admin-curated marketplace catalog.
// Config is kind-specific: for KindMCPServer it holds the OpenHands-native
// MCPServer fields (command/args/env for stdio, or url/transport for a
// remote server) that apps/agent-engine/internal/marketplaceclient decodes.
type Entry struct {
	ID          uuid.UUID
	Kind        Kind
	Name        string
	Description string
	Config      json.RawMessage
	CreatedBy   uuid.UUID
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// TenantEntry is one per-tenant enablement row: Entry (by ID) is enabled for
// a tenant, recorded by whom and when.
type TenantEntry struct {
	EntryID   uuid.UUID
	EnabledBy uuid.UUID
	EnabledAt time.Time
}

var (
	// ErrNotFound is returned when the requested catalog entry does not exist.
	ErrNotFound = errors.New("marketplace: entry not found")

	// ErrInvalidKind is returned when Kind.Valid() is false.
	ErrInvalidKind = errors.New("marketplace: kind must be one of mcp_server, rule, skill, prompt_template")

	// ErrInvalidName is returned when name is empty after trimming whitespace.
	ErrInvalidName = errors.New("marketplace: name must not be empty")

	// ErrInvalidConfig is returned when config is present but not a valid JSON object.
	ErrInvalidConfig = errors.New("marketplace: config must be a valid JSON object")

	// ErrDuplicate is returned when an entry with the same (kind, name) already exists.
	ErrDuplicate = errors.New("marketplace: an entry with that kind and name already exists")
)
