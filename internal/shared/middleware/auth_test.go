package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

const testSecret = "test-secret"

func generateTestToken(userID string, expiry time.Time) string {
	claims := middleware.Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiry),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "money-management",
		},
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	return token
}

func setupAuthRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.Auth(testSecret))
	r.GET("/protected", func(c *gin.Context) {
		userID := middleware.GetUserID(c.Request.Context())
		c.JSON(http.StatusOK, gin.H{"user_id": userID})
	})
	r.GET("/swagger/index.html", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"page": "swagger"})
	})
	r.POST("/auth/register", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"page": "register"})
	})
	r.POST("/auth/login", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"page": "login"})
	})
	return r
}

func TestAuth_ValidToken(t *testing.T) {
	router := setupAuthRouter()
	token := generateTestToken("user-123", time.Now().Add(time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
}

func TestAuth_MissingHeader(t *testing.T) {
	router := setupAuthRouter()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAuth_InvalidToken(t *testing.T) {
	router := setupAuthRouter()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAuth_ExpiredToken(t *testing.T) {
	router := setupAuthRouter()
	token := generateTestToken("user-123", time.Now().Add(-time.Hour))

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}

func TestAuth_PublicPaths(t *testing.T) {
	router := setupAuthRouter()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"swagger", http.MethodGet, "/swagger/index.html"},
		{"register", http.MethodPost, "/auth/register"},
		{"login", http.MethodPost, "/auth/login"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
			}
		})
	}
}

func TestAuth_InvalidFormat(t *testing.T) {
	router := setupAuthRouter()

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "NotBearer some-token")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}
}
