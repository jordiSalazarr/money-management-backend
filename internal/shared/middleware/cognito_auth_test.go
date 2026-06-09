package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newRouter(publicPaths []string, source middleware.ClaimsSource) *gin.Engine {
	r := gin.New()
	r.Use(middleware.CognitoAuth(publicPaths, source))
	r.GET("/me", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"user_id": middleware.GetUserID(c.Request.Context())})
	})
	r.GET("/swagger/ping", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestCognitoAuth_AllowsPublicPath(t *testing.T) {
	source := func(context.Context) (map[string]string, bool) { return nil, false }
	r := newRouter([]string{"/swagger/"}, source)

	req := httptest.NewRequest(http.MethodGet, "/swagger/ping", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("public path status = %d, want 200", w.Code)
	}
}

func TestCognitoAuth_AcceptsValidClaims(t *testing.T) {
	source := func(context.Context) (map[string]string, bool) {
		return map[string]string{"sub": "user-abc"}, true
	}
	r := newRouter(nil, source)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), `"user_id":"user-abc"`) {
		t.Errorf("body = %s, want user_id=user-abc", w.Body.String())
	}
}

func TestCognitoAuth_RejectsMissingClaimsAndHeader(t *testing.T) {
	source := func(context.Context) (map[string]string, bool) { return nil, false }
	r := newRouter(nil, source)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestCognitoAuth_RejectsEmptySubClaim(t *testing.T) {
	source := func(context.Context) (map[string]string, bool) {
		return map[string]string{"email": "alice@example.com"}, true // no sub
	}
	r := newRouter(nil, source)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (sub missing)", w.Code)
	}
}

func TestCognitoAuth_LocalDevFallback(t *testing.T) {
	source := func(context.Context) (map[string]string, bool) { return nil, false }
	r := newRouter(nil, source)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("X-Dev-User", "dev-user-42")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	if !contains(w.Body.String(), `"user_id":"dev-user-42"`) {
		t.Errorf("body = %s, want user_id=dev-user-42", w.Body.String())
	}
}

func TestCognitoAuth_APIGWClaimsPreferredOverHeader(t *testing.T) {
	// When both API GW claims AND X-Dev-User are present, claims win —
	// preventing a Lambda-mode attacker from spoofing identity via header.
	source := func(context.Context) (map[string]string, bool) {
		return map[string]string{"sub": "from-jwt"}, true
	}
	r := newRouter(nil, source)

	req := httptest.NewRequest(http.MethodGet, "/me", nil)
	req.Header.Set("X-Dev-User", "from-header")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !contains(w.Body.String(), `"user_id":"from-jwt"`) {
		t.Errorf("body = %s, want user_id=from-jwt", w.Body.String())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
