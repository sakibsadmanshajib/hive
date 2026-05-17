package signup_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/hivegpt/hive/apps/control-plane/internal/signup"
)

func TestResolver_InviteTokenWins(t *testing.T) {
	want := uuid.New()
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) {
			if tok != "tok-abc" {
				return uuid.Nil, signup.ErrNoMatch
			}
			return want, nil
		},
	})
	got, err := r.Resolve(context.Background(), signup.Input{
		InviteToken: "tok-abc",
		Email:       "x@y.example",
	})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolver_EmailDomainFallback(t *testing.T) {
	want := uuid.New()
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) {
			return uuid.Nil, signup.ErrNoMatch
		},
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			if domain != "office.example" {
				return uuid.Nil, signup.ErrNoMatch
			}
			return want, nil
		},
	})
	got, err := r.Resolve(context.Background(), signup.Input{Email: "user@office.example"})
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestResolver_NoneMatch_ReturnsErrNoMatch(t *testing.T) {
	r := signup.NewResolver(signup.ResolverDeps{
		InviteLookup: func(ctx context.Context, tok string) (uuid.UUID, error) {
			return uuid.Nil, signup.ErrNoMatch
		},
		DomainLookup: func(ctx context.Context, domain string) (uuid.UUID, error) {
			return uuid.Nil, signup.ErrNoMatch
		},
	})
	_, err := r.Resolve(context.Background(), signup.Input{Email: "stranger@unknown.example"})
	require.True(t, errors.Is(err, signup.ErrNoMatch))
}
