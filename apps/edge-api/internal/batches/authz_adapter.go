package batches

import (
	"fmt"
	"net/http"

	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
)

// AuthorizerAdapter adapts authz.Authorizer to the batches.Authorizer interface.
type AuthorizerAdapter struct {
	inner *authz.Authorizer
}

// NewAuthorizerAdapter wraps an authz.Authorizer for use with the batches Handler.
func NewAuthorizerAdapter(inner *authz.Authorizer) *AuthorizerAdapter {
	return &AuthorizerAdapter{inner: inner}
}

// AuthorizeRequest extracts the Authorization header and delegates to the inner authorizer.
func (a *AuthorizerAdapter) AuthorizeRequest(r *http.Request) (AuthResult, error) {
	authHeader := r.Header.Get("Authorization")
	snapshot, _, authErr := a.inner.Authorize(r.Context(), authHeader, "", 0, 0, 0)
	if authErr != nil {
		return AuthResult{}, fmt.Errorf("unauthorized: %s", authErr.Error.Message)
	}
	return AuthResult{
		AccountID: snapshot.AccountID,
		APIKeyID:  snapshot.KeyID,
	}, nil
}
