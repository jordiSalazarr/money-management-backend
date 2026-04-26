package register_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/auth"
	"github.com/jordisalazar/money-management/auth/register"
)

func setupRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := auth.NewMemoryUserStore()
	register.RegisterRoutes(r, store)
	return r
}

func TestRegister_Success(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"email":    "test@example.com",
		"password": "securepass123",
	}
	jsonBody, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusCreated, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["user_id"] == "" {
		t.Error("expected user_id in response")
	}
}

func TestRegister_DuplicateEmail(t *testing.T) {
	router := setupRouter()

	body := map[string]any{
		"email":    "dup@example.com",
		"password": "securepass123",
	}
	jsonBody, _ := json.Marshal(body)

	// First registration
	req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("first register: status = %d, want %d", w.Code, http.StatusCreated)
	}

	// Second registration with same email
	jsonBody, _ = json.Marshal(body)
	req = httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusConflict, w.Body.String())
	}

	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error_code"] != "email_taken" {
		t.Errorf("error_code = %q, want %q", resp["error_code"], "email_taken")
	}
}

func TestRegister_ValidationErrors(t *testing.T) {
	router := setupRouter()

	tests := []struct {
		name     string
		body     map[string]any
		wantCode string
	}{
		{
			name:     "missing email",
			body:     map[string]any{"password": "securepass123"},
			wantCode: "missing_email",
		},
		{
			name:     "short password",
			body:     map[string]any{"email": "test@example.com", "password": "short"},
			wantCode: "password_too_short",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/auth/register", bytes.NewReader(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
			}

			var resp map[string]string
			json.Unmarshal(w.Body.Bytes(), &resp)
			if resp["error_code"] != tt.wantCode {
				t.Errorf("error_code = %q, want %q", resp["error_code"], tt.wantCode)
			}
		})
	}
}
