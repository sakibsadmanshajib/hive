package auth

import (
	"context"

	"github.com/google/uuid"
)

// Viewer represents the authenticated Supabase user resolved from a bearer token.
type Viewer struct {
	UserID        uuid.UUID
	Email         string
	EmailVerified bool
	FullName      string
}

// contextKey is an unexported type for context keys in this package.
type contextKey int

const viewerKey contextKey = iota

// WithViewer stores a Viewer in the context.
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return context.WithValue(ctx, viewerKey, v)
}

// ViewerFromContext retrieves the Viewer from the context.
// Returns the zero Viewer and false if not present.
func ViewerFromContext(ctx context.Context) (Viewer, bool) {
	v, ok := ctx.Value(viewerKey).(Viewer)
	return v, ok
}
