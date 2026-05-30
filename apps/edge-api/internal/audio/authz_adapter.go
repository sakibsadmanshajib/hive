package audio

import (
	"net/http"

	"github.com/sakibsadmanshajib/hive/apps/edge-api/internal/authz"
)

// AuthorizerAdapter adapts authz.Authorizer to the audio.Authorizer interface.
type AuthorizerAdapter struct {
	inner *authz.Authorizer
}

// NewAuthorizerAdapter wraps an authz.Authorizer for use with the audio Handler.
func NewAuthorizerAdapter(inner *authz.Authorizer) *AuthorizerAdapter {
	return &AuthorizerAdapter{inner: inner}
}

// AuthorizeRequest extracts the Authorization header and delegates to the inner authorizer.
func (a *AuthorizerAdapter) AuthorizeRequest(r *http.Request) (AuthResult, error) {
	authHeader := r.Header.Get("Authorization")
	snapshot, headers, authErr := a.inner.Authorize(r.Context(), authHeader, "", 0, 0, 0)
	if authErr != nil {
		return AuthResult{}, &authz.AuthzError{OpenAIErr: authErr, Headers: headers}
	}
	return AuthResult{
		AccountID: snapshot.AccountID,
		APIKeyID:  snapshot.KeyID,
	}, nil
}
