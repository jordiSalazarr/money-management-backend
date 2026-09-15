package middleware_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

const (
	testIssuerPath = "/realms/money"
	testAudience   = "money-management"
	testKid        = "test-key-1"
)

// idp is a minimal OIDC provider: discovery document, JWKS, and a way to mint
// tokens. A real one differs only in hostname, which is the point — the
// verifier talks OIDC, not Cognito or Keycloak.
type idp struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newIDP(t *testing.T) *idp {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}

	p := &idp{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc(testIssuerPath+"/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer":   p.issuer(),
			"jwks_uri": p.server.URL + testIssuerPath + "/jwks",
		})
	})
	mux.HandleFunc(testIssuerPath+"/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]string{{
				"kty": "RSA",
				"kid": testKid,
				"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			}},
		})
	})

	p.server = httptest.NewServer(mux)
	t.Cleanup(p.server.Close)
	return p
}

func (p *idp) issuer() string { return p.server.URL + testIssuerPath }

func (p *idp) token(t *testing.T, mutate func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "11111111-1111-1111-1111-111111111111",
		"iss": p.issuer(),
		"aud": testAudience,
		"exp": time.Now().Add(time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKid
	signed, err := token.SignedString(p.key)
	if err != nil {
		t.Fatalf("signing: %v", err)
	}
	return signed
}

func routerWith(verifier *middleware.OIDCVerifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Auth([]string{"/health/"}, verifier))
	r.GET("/whoami", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": middleware.GetUserID(c.Request.Context())})
	})
	r.GET("/health/live", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	return r
}

func call(r *gin.Engine, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func verifierFor(p *idp) *middleware.OIDCVerifier {
	return middleware.NewOIDCVerifier(middleware.OIDCConfig{
		Issuer:     p.issuer(),
		Audience:   testAudience,
		HTTPClient: p.server.Client(),
	})
}

func TestAValidTokenAuthenticates(t *testing.T) {
	p := newIDP(t)
	w := call(routerWith(verifierFor(p)), map[string]string{
		"Authorization": "Bearer " + p.token(t, nil),
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["user_id"] != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("user_id = %q, want the token's sub", resp["user_id"])
	}
}

// TestTheDevHeaderIsRefusedOnceOIDCIsOn is the property the whole design rests
// on.
//
// X-Dev-User is self-asserted: anyone can claim to be anyone. It is fine when
// it is the only mechanism and the API is on a laptop. It is a total
// authentication bypass the moment the API is reachable, so enabling OIDC must
// DISABLE it — not fall back to it.
func TestTheDevHeaderIsRefusedOnceOIDCIsOn(t *testing.T) {
	p := newIDP(t)
	w := call(routerWith(verifierFor(p)), map[string]string{
		"X-Dev-User": "22222222-2222-2222-2222-222222222222",
	})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — the dev header must not authenticate anyone once OIDC is on", w.Code)
	}
}

// And a token that cannot be verified must not fall back to the header either.
func TestABadTokenDoesNotFallBackToTheDevHeader(t *testing.T) {
	p := newIDP(t)
	w := call(routerWith(verifierFor(p)), map[string]string{
		"Authorization": "Bearer not-a-token",
		"X-Dev-User":    "22222222-2222-2222-2222-222222222222",
	})

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 — a failed verification must fail, not degrade", w.Code)
	}
}

func TestTokensAreRejected(t *testing.T) {
	p := newIDP(t)
	other := newIDP(t)

	cases := []struct {
		name  string
		token func(t *testing.T) string
	}{
		{
			name: "expired",
			token: func(t *testing.T) string {
				return p.token(t, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
			},
		},
		{
			// A token for a different client of the same issuer is not a token
			// for us.
			name:  "wrong audience",
			token: func(t *testing.T) string { return p.token(t, func(c jwt.MapClaims) { c["aud"] = "some-other-app" }) },
		},
		{
			name: "wrong issuer",
			token: func(t *testing.T) string {
				return p.token(t, func(c jwt.MapClaims) { c["iss"] = "https://evil.example" })
			},
		},
		{
			// Correctly formed and correctly signed — by the wrong key.
			name:  "signed by another provider",
			token: func(t *testing.T) string { return other.token(t, func(c jwt.MapClaims) { c["iss"] = p.issuer() }) },
		},
		{
			name:  "no subject",
			token: func(t *testing.T) string { return p.token(t, func(c jwt.MapClaims) { delete(c, "sub") }) },
		},
		{
			name: "alg none",
			token: func(t *testing.T) string {
				// The classic JWT hole: a token that declares it needs no
				// signature. Verification must reject it on the algorithm,
				// before it ever looks at the claims.
				tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
					"sub": "attacker", "iss": p.issuer(), "aud": testAudience,
					"exp": time.Now().Add(time.Hour).Unix(),
				})
				signed, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
				return signed
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := call(routerWith(verifierFor(p)), map[string]string{"Authorization": "Bearer " + c.token(t)})
			if w.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401; body = %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestWithoutAnIssuerTheDevHeaderWorks(t *testing.T) {
	w := call(routerWith(nil), map[string]string{
		"X-Dev-User": "33333333-3333-3333-3333-333333333333",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — local dev needs the header", w.Code)
	}
}

func TestProbesNeedNoCredentials(t *testing.T) {
	p := newIDP(t)
	req := httptest.NewRequest(http.MethodGet, "/health/live", nil)
	w := httptest.NewRecorder()
	routerWith(verifierFor(p)).ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — kubelet sends no credentials", w.Code)
	}
}
