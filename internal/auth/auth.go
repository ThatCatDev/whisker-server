// Package auth verifies Supabase-issued JWTs. Supabase signs access tokens
// with the project's JWT secret (HS256); verification is local — no network
// call per request.
package auth

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

var ErrUnauthorized = errors.New("auth: unauthorized")

type Auth struct {
	secret []byte
	// Disabled skips verification entirely and treats everyone as the same
	// dev user. For local development only.
	disabled bool
}

// New builds a verifier from the Supabase JWT secret (Project Settings →
// API → JWT Secret). An empty secret with disabled=false refuses everything.
func New(secret string, disabled bool) *Auth {
	return &Auth{secret: []byte(secret), disabled: disabled}
}

// UserID extracts and verifies the caller's identity. Tokens are accepted
// from the Authorization header (REST) or the `token` query parameter
// (websockets, where browsers cannot set headers).
func (a *Auth) UserID(r *http.Request) (string, error) {
	if a.disabled {
		return "dev-user", nil
	}
	token := r.URL.Query().Get("token")
	if h := r.Header.Get("Authorization"); h != "" {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if token == "" {
		return "", fmt.Errorf("%w: missing token", ErrUnauthorized)
	}
	return a.verify(token)
}

func (a *Auth) verify(token string) (string, error) {
	if len(a.secret) == 0 {
		return "", fmt.Errorf("%w: server has no JWT secret configured", ErrUnauthorized)
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return a.secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	sub, err := parsed.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", fmt.Errorf("%w: token has no subject", ErrUnauthorized)
	}
	return sub, nil
}
