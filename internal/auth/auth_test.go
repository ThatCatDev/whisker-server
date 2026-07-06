package auth

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func jwksServer(t *testing.T, keys []map[string]string) *httptest.Server {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"keys": keys})
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
}

func requestWithToken(token string) *http.Request {
	r := httptest.NewRequest("GET", "/api/boards", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, kid, sub string, exp time.Time) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"sub": sub,
		"exp": exp.Unix(),
	})
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestJWKSES256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := key.PublicKey
	srv := jwksServer(t, []map[string]string{{
		"kty": "EC", "crv": "P-256", "kid": "key-1",
		"x": b64(pub.X.FillBytes(make([]byte, 32))),
		"y": b64(pub.Y.FillBytes(make([]byte, 32))),
	}})
	defer srv.Close()

	a := New("", srv.URL, false)
	token := signES256(t, key, "key-1", "user-42", time.Now().Add(time.Hour))
	sub, err := a.UserID(requestWithToken(token))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sub != "user-42" {
		t.Fatalf("sub = %q", sub)
	}

	// Expired token is refused.
	expired := signES256(t, key, "key-1", "user-42", time.Now().Add(-time.Hour))
	if _, err := a.UserID(requestWithToken(expired)); err == nil {
		t.Fatal("expired token accepted")
	}

	// Unknown kid is refused (refetch is rate-limited inside the window).
	wrongKid := signES256(t, key, "key-2", "user-42", time.Now().Add(time.Hour))
	if _, err := a.UserID(requestWithToken(wrongKid)); err == nil {
		t.Fatal("unknown kid accepted")
	}
}

func TestJWKSEd25519(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := jwksServer(t, []map[string]string{{
		"kty": "OKP", "crv": "Ed25519", "kid": "ed-1", "x": b64(pub),
	}})
	defer srv.Close()

	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
		"sub": "user-ed",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "ed-1"
	signed, err := tok.SignedString(priv)
	if err != nil {
		t.Fatal(err)
	}

	a := New("", srv.URL, false)
	sub, err := a.UserID(requestWithToken(signed))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sub != "user-ed" {
		t.Fatalf("sub = %q", sub)
	}
}

func TestHS256StillWorks(t *testing.T) {
	secret := "test-secret-at-least-32-characters!!"
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "user-hs",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, _ := tok.SignedString([]byte(secret))

	a := New(secret, "", false)
	sub, err := a.UserID(requestWithToken(signed))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sub != "user-hs" {
		t.Fatalf("sub = %q", sub)
	}

	// An asymmetric token against an HS256-only verifier is refused.
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	es := signES256(t, key, "kid", "user-x", time.Now().Add(time.Hour))
	if _, err := a.UserID(requestWithToken(es)); err == nil {
		t.Fatal("ES256 token accepted without JWKS")
	}
}
