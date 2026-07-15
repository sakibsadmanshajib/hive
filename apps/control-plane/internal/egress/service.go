package egress

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
)

// OwnerChecker is the narrow port the service uses to verify the caller owns
// the workspace being configured. *platform.RoleService satisfies this
// interface structurally — accepting the interface here (rather than the
// concrete type) avoids a package dependency and keeps unit tests DB-free
// (mirrors internal/grants/service.go's AdminChecker).
type OwnerChecker interface {
	IsWorkspaceOwner(ctx context.Context, userID, workspaceID uuid.UUID) (bool, error)
}

// Service holds the egress-policy business logic: host-list validation, the
// tenant-default/user-override merge, and the workspace-owner write gate.
type Service struct {
	repo  Repository
	owner OwnerChecker
}

// NewService constructs the egress policy service.
func NewService(repo Repository, owner OwnerChecker) *Service {
	return &Service{repo: repo, owner: owner}
}

// Effective resolves the policy that actually applies to tenantID+userID: a
// per-user override replaces the tenant default outright (no merge — the
// admin who sets a user override is making an explicit, complete decision
// for that user, not layering on top of the tenant baseline). Falling back to
// the tenant default, and further to an empty allowlist (implicit deny-all)
// when neither row exists, keeps the resolution total: this is the single
// call both the OpenHands allowed_hosts config and the desktop firewall rule
// generator make (issue #308).
func (s *Service) Effective(ctx context.Context, tenantID, userID uuid.UUID) (Policy, error) {
	if userID != uuid.Nil {
		override, err := s.repo.GetUserOverride(ctx, tenantID, userID)
		if err == nil {
			return override, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Policy{}, err
		}
	}

	def, err := s.repo.GetTenantDefault(ctx, tenantID)
	if err == nil {
		return def, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Policy{}, err
	}

	// Neither a user override nor a tenant default exists: fail closed.
	return Policy{TenantID: tenantID, UserID: userID}, nil
}

// GetTenantDefault returns the tenant-wide default row. Owner-gated.
func (s *Service) GetTenantDefault(ctx context.Context, callerID, tenantID uuid.UUID) (Policy, error) {
	if err := s.requireOwner(ctx, callerID, tenantID); err != nil {
		return Policy{}, err
	}
	return s.repo.GetTenantDefault(ctx, tenantID)
}

// GetUserOverride returns a single user's override row (not merged with the
// tenant default — callers that want the resolved policy call Effective).
// Owner-gated.
func (s *Service) GetUserOverride(ctx context.Context, callerID, tenantID, targetUserID uuid.UUID) (Policy, error) {
	if err := s.requireOwner(ctx, callerID, tenantID); err != nil {
		return Policy{}, err
	}
	return s.repo.GetUserOverride(ctx, tenantID, targetUserID)
}

// SetTenantDefault validates hosts and upserts the tenant-wide default.
// Owner-gated.
func (s *Service) SetTenantDefault(ctx context.Context, callerID, tenantID uuid.UUID, hosts []string) (Policy, error) {
	if err := s.requireOwner(ctx, callerID, tenantID); err != nil {
		return Policy{}, err
	}
	clean, err := normalizeHosts(hosts)
	if err != nil {
		return Policy{}, err
	}
	return s.repo.UpsertTenantDefault(ctx, tenantID, clean)
}

// SetUserOverride validates hosts and upserts a per-user override. Owner-gated.
func (s *Service) SetUserOverride(ctx context.Context, callerID, tenantID, targetUserID uuid.UUID, hosts []string) (Policy, error) {
	if targetUserID == uuid.Nil {
		return Policy{}, errors.New("egress: target user_id required")
	}
	if err := s.requireOwner(ctx, callerID, tenantID); err != nil {
		return Policy{}, err
	}
	clean, err := normalizeHosts(hosts)
	if err != nil {
		return Policy{}, err
	}
	return s.repo.UpsertUserOverride(ctx, tenantID, targetUserID, clean)
}

// DeleteUserOverride removes a per-user override, reverting that user to the
// tenant default on their next Effective lookup. Owner-gated.
func (s *Service) DeleteUserOverride(ctx context.Context, callerID, tenantID, targetUserID uuid.UUID) error {
	if err := s.requireOwner(ctx, callerID, tenantID); err != nil {
		return err
	}
	return s.repo.DeleteUserOverride(ctx, tenantID, targetUserID)
}

func (s *Service) requireOwner(ctx context.Context, callerID, tenantID uuid.UUID) error {
	isOwner, err := s.owner.IsWorkspaceOwner(ctx, callerID, tenantID)
	if err != nil {
		return err
	}
	if !isOwner {
		return ErrForbidden
	}
	return nil
}

// normalizeHosts trims, drops empties, and dedupes hostnames while rejecting
// any entry that still contains whitespace after trimming (a host/glob
// pattern has no legitimate use for embedded whitespace; catching it here is
// cheaper than debugging a firewall rule generator choking on it later).
func normalizeHosts(hosts []string) ([]string, error) {
	seen := make(map[string]struct{}, len(hosts))
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if strings.ContainsAny(h, " \t\n\r") {
			return nil, ErrInvalidHosts
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out, nil
}
