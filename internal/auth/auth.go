// Package auth verifies Supabase-issued JWTs. Legacy projects sign access
// tokens with the project's JWT secret (HS256); projects on the newer "JWT
// signing keys" use asymmetric keys (ES256/RS256) published as a JWKS.
// Both verify locally / against cached keys — no network call per request.
package auth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrUnauthorized = errors.New("auth: unauthorized")

type Auth struct {
	secret  []byte
	jwksURL string
	// Disabled skips verification entirely and treats everyone as the same
	// dev user. For local development only.
	disabled bool

	mu        sync.Mutex
	keys      map[string]crypto.PublicKey // by kid
	lastFetch time.Time
}

// New builds a verifier. `secret` is the legacy HS256 JWT secret (may be
// empty), `jwksURL` the JWKS endpoint for asymmetric signing keys (may be
// empty). With neither configured and disabled=false, everything is refused.
func New(secret, jwksURL string, disabled bool) *Auth {
	return &Auth{
		secret:   []byte(secret),
		jwksURL:  jwksURL,
		disabled: disabled,
		keys:     map[string]crypto.PublicKey{},
	}
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
	if len(a.secret) == 0 && a.jwksURL == "" {
		return "", fmt.Errorf("%w: server has no JWT secret or JWKS configured", ErrUnauthorized)
	}
	parsed, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		switch t.Method.(type) {
		case *jwt.SigningMethodHMAC:
			if len(a.secret) == 0 {
				return nil, errors.New("HS256 token but no JWT secret configured")
			}
			return a.secret, nil
		case *jwt.SigningMethodECDSA, *jwt.SigningMethodRSA, *jwt.SigningMethodEd25519:
			if a.jwksURL == "" {
				return nil, errors.New("asymmetric token but no JWKS configured")
			}
			kid, _ := t.Header["kid"].(string)
			return a.keyFor(kid)
		default:
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
	}, jwt.WithValidMethods([]string{"HS256", "ES256", "RS256", "EdDSA"}), jwt.WithExpirationRequired())
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnauthorized, err)
	}
	sub, err := parsed.Claims.GetSubject()
	if err != nil || sub == "" {
		return "", fmt.Errorf("%w: token has no subject", ErrUnauthorized)
	}
	return sub, nil
}

// keyFor returns the cached public key for kid, refetching the JWKS at most
// once a minute when an unknown kid shows up (key rotation).
func (a *Auth) keyFor(kid string) (crypto.PublicKey, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	if time.Since(a.lastFetch) < time.Minute {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	if err := a.fetchLocked(); err != nil {
		return nil, err
	}
	if k, ok := a.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown key id %q", kid)
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func (a *Auth) fetchLocked() error {
	a.lastFetch = time.Now()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(a.jwksURL)
	if err != nil {
		return fmt.Errorf("jwks fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks fetch: %s", resp.Status)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("jwks parse: %w", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		pub, err := k.publicKey()
		if err != nil {
			continue // skip unsupported key types (e.g. the HS256 entry)
		}
		keys[k.Kid] = pub
	}
	a.keys = keys
	return nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	b64 := func(s string) (*big.Int, error) {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			return nil, err
		}
		return new(big.Int).SetBytes(b), nil
	}
	switch k.Kty {
	case "EC":
		if k.Crv != "P-256" {
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		x, err := b64(k.X)
		if err != nil {
			return nil, err
		}
		y, err := b64(k.Y)
		if err != nil {
			return nil, err
		}
		return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
	case "RSA":
		n, err := b64(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64(k.E)
		if err != nil {
			return nil, err
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "OKP": // Ed25519 ("EdDSA coming soon" per Supabase docs)
		if k.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported curve %q", k.Crv)
		}
		b, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, err
		}
		return ed25519.PublicKey(b), nil
	default:
		return nil, fmt.Errorf("unsupported key type %q", k.Kty)
	}
}
