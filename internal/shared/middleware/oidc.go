package middleware

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"crypto/rsa"

	"github.com/golang-jwt/jwt/v5"
)

// OIDCConfig turns the identity provider into a configuration choice.
//
// The point of verifying against a discovered JWKS rather than a provider SDK
// is that Cognito, Keycloak and anything else OIDC-compliant differ only in
// these two strings. Picking a provider becomes a deployment decision instead
// of a code change — which is what lets the decision stay open.
type OIDCConfig struct {
	// Issuer is the `iss` every token must carry, and where discovery starts.
	// Empty turns verification off and puts the API in local-dev mode.
	Issuer string
	// Audience is the `aud` every token must carry. A token minted for a
	// different client of the same issuer is not a token for us.
	Audience string
	// HTTPClient is injectable so tests can serve their own JWKS.
	HTTPClient *http.Client
}

func (c OIDCConfig) Enabled() bool { return c.Issuer != "" }

// OIDCVerifier validates tokens against the issuer's published keys.
type OIDCVerifier struct {
	config OIDCConfig
	client *http.Client

	mu       sync.RWMutex
	keys     map[string]*rsa.PublicKey
	jwksURI  string
	fetched  time.Time
	cacheFor time.Duration
}

func NewOIDCVerifier(config OIDCConfig) *OIDCVerifier {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &OIDCVerifier{
		config:   config,
		client:   client,
		keys:     make(map[string]*rsa.PublicKey),
		cacheFor: time.Hour,
	}
}

// Verify checks the signature, issuer, audience and expiry, and returns the
// subject.
//
// It returns only an error, never a partially-trusted result: a token that
// fails any check is not a token.
func (v *OIDCVerifier) Verify(ctx context.Context, raw string) (string, error) {
	token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		// Pin the algorithm family. Accepting whatever the token names is the
		// classic JWT hole: a token claiming "alg":"none", or an HMAC token
		// verified with the RSA public key as its shared secret, would both
		// sail through otherwise.
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		return v.keyFor(ctx, kid)
	},
		jwt.WithIssuer(v.config.Issuer),
		jwt.WithAudience(v.config.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
	)
	if err != nil {
		return "", fmt.Errorf("verifying token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("unexpected claims type %T", token.Claims)
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return "", fmt.Errorf("token has no subject")
	}
	return sub, nil
}

// keyFor returns the signing key for a kid, refreshing the key set once if the
// kid is unknown.
//
// The single refresh matters: providers rotate keys, and a cached set that is
// never refreshed would start rejecting every token the moment they do. One
// refresh per unknown kid — rather than per failure — keeps a flood of bad
// tokens from turning into a flood of outbound requests.
func (v *OIDCVerifier) keyFor(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.RLock()
	key, ok := v.keys[kid]
	fresh := time.Since(v.fetched) < v.cacheFor
	v.mu.RUnlock()

	if ok {
		return key, nil
	}
	if fresh && len(v.keys) > 0 {
		// The set is current and does not contain this kid, so the token was
		// signed by something else entirely.
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}

	if err := v.refresh(ctx); err != nil {
		return nil, err
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (v *OIDCVerifier) refresh(ctx context.Context) error {
	uri, err := v.discover(ctx)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return fmt.Errorf("building jwks request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetching jwks: %s", resp.Status)
	}

	var doc struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decoding jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := rsaKeyFrom(k.N, k.E)
		if err != nil {
			// One malformed key must not discard the rest of the set.
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("jwks at %s contained no usable RSA keys", uri)
	}

	v.mu.Lock()
	v.keys = keys
	v.fetched = time.Now()
	v.mu.Unlock()
	return nil
}

// discover reads the issuer's OIDC metadata to find where its keys live.
// Hard-coding the path would work for most providers and break on the rest.
func (v *OIDCVerifier) discover(ctx context.Context) (string, error) {
	v.mu.RLock()
	cached := v.jwksURI
	v.mu.RUnlock()
	if cached != "" {
		return cached, nil
	}

	url := strings.TrimSuffix(v.config.Issuer, "/") + "/.well-known/openid-configuration"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("building discovery request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: %s", url, resp.Status)
	}

	var doc struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", fmt.Errorf("decoding discovery document: %w", err)
	}
	if doc.JWKSURI == "" {
		return "", fmt.Errorf("discovery document at %s has no jwks_uri", url)
	}

	v.mu.Lock()
	v.jwksURI = doc.JWKSURI
	v.mu.Unlock()
	return doc.JWKSURI, nil
}

// rsaKeyFrom rebuilds a public key from the base64url modulus and exponent a
// JWKS publishes.
func rsaKeyFrom(nB64, eB64 string) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(nB64, "="))
	if err != nil {
		return nil, fmt.Errorf("decoding modulus: %w", err)
	}
	e, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(eB64, "="))
	if err != nil {
		return nil, fmt.Errorf("decoding exponent: %w", err)
	}

	// The exponent is a big-endian integer of whatever length the provider
	// chose — commonly three bytes, but not guaranteed — so left-pad to a
	// uint32 rather than assuming.
	if len(e) > 4 {
		return nil, fmt.Errorf("exponent too large")
	}
	padded := make([]byte, 4)
	copy(padded[4-len(e):], e)

	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(n),
		E: int(binary.BigEndian.Uint32(padded)),
	}, nil
}
