package files

import (
	"fmt"
	"net/http"

	"github.com/hivegpt/hive/apps/edge-api/internal/authz"
)

// AuthorizerAdapter adapts authz.Authorizer to the files.Authorizer interface.
// It calls Authorize with empty aliasID and zero credit values, since Files/Uploads
// endpoints do not route to providers and do not need pre-flight credit checks.
type AuthorizerAdapter struct {
	inner *authz.Authorizer
}

// NewAuthorizerAdapter wraps an authz.Authorizer for use with the files Handler.
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
