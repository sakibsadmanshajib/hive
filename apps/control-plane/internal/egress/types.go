// Package egress owns the single source of truth for agent-sandbox network
// egress allowlists (issue #308). One control-plane-owned policy store, admin
// configurable per tenant AND per user, exposed over HTTP for two future
// consumers that must never drift apart the way the HIVE_SOVEREIGN guard did
// (see #245):
//
//   - Server-side OpenHands workspace config (`allowed_hosts`), wave 2.
//   - Desktop firewall rule generator, wave 4.
//
// This package builds the store, the admin CRUD surface, and the read
// endpoint only. Neither consumer is wired here.
package egress

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Policy is one row of egress allowlist configuration. UserID is uuid.Nil for
// the tenant-wide default row; a non-nil UserID is a per-user override.
type Policy struct {
	TenantID     uuid.UUID
	UserID       uuid.UUID
	AllowedHosts []string
	UpdatedAt    time.Time
}

// IsTenantDefault reports whether p is the tenant-wide default row.
func (p Policy) IsTenantDefault() bool { return p.UserID == uuid.Nil }

var (
	// ErrNotFound is returned when no policy row exists for the requested scope.
	ErrNotFound = errors.New("egress: policy not found")

	// ErrForbidden is returned when the caller is not the workspace owner.
	ErrForbidden = errors.New("egress: caller is not the workspace owner")

	// ErrInvalidHosts is returned when the submitted allowed-hosts list fails
	// validation (empty entries or entries containing whitespace).
	ErrInvalidHosts = errors.New("egress: allowed_hosts entries must be non-empty and contain no whitespace")
)
