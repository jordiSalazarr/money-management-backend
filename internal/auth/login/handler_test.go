package login_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/auth"
	"github.com/jordisalazar/money-management/internal/auth/login"
	"github.com/jordisalazar/money-management/internal/auth/register"
)

const testSecret = "test-secret"

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := auth.NewMemoryUserStore()
	register.RegisterRoutes(r, store)
	login.RegisterRoutes(r, store, testSecret)
	return r
}

func registerUser(t *testing.T, router *gin.Engine, email, password string) {
	t.Helper()
	body := map[string]any{"email": email, "password": password}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("register: status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}
}

func TestLogin_Success(t *testing.T) {
	router := setupRouter()
	registerUser(t, router, "test@example.com", "securepass123")

	body := map[string]any{
		"email":    "test@example.com",
		"password": "securepass123",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["token"] == "" {
		t.Error("expected token in response")
	}
	if resp["user_id"] == "" {
		t.Error("expected user_id in response")
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	router := setupRouter()
	registerUser(t, router, "test@example.com", "securepass123")

	body := map[string]any{
		"email":    "test@example.com",
		"password": "wrongpassword",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "invalid_credentials" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "invalid_credentials")
	}
}

func TestLogin_UnknownEmail(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"email":    "unknown@example.com",
		"password": "securepass123",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusUnauthorized, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "invalid_credentials" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "invalid_credentials")
	}
}
