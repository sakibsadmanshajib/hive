// Package usermemories implements the cross-chat user memory slice (issue
// #172, ruling D-020): a thin Hive-owned store plus the four-verb internal
// API (create/list/update/delete). Recall reads these rows in the edge-api
// chat dispatch path; automatic extraction from conversations is a later
// wave, so this package is write-API only.
package usermemories

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Memory is one stored fact about a single user inside one tenant. Tenant
// and user never serialize: both are implied by the URL path segment pair
// every request is addressed with, mirroring agenttask's wire shapes.
type Memory struct {
	ID           uuid.UUID `json:"id"`
	TenantID     uuid.UUID `json:"-"`
	UserID       uuid.UUID `json:"-"`
	Content      string    `json:"content"`
	SourceChatID *string   `json:"source_chat_id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Service-layer bounds for this slice.
const (
	// MaxContentLen caps stored content at the boundary. The CHECK on
	// public.user_memories enforces the same number at storage level.
	MaxContentLen = 500
	// MaxMemoriesPerUser evicts oldest-beyond-this on create.
	MaxMemoriesPerUser = 100
)

var (
	// ErrNotFound is returned when no memory exists for the given id within
	// the caller's (tenant, user) scope. Cross-tenant and cross-user access
	// collapse into this error by design: both read as 404 outside.
	ErrNotFound = errors.New("usermemories: memory not found")

	// ErrEmptyContent is returned when content is empty after sanitization.
	ErrEmptyContent = errors.New("usermemories: content is empty")
)
