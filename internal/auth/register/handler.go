package register

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	store auth.UserStorer
}

func NewHandler(store auth.UserStorer) *Handler {
	return &Handler{store: store}
}

// Request is the payload for user registration.
type Request struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"securepass123"`
}

// Response is the result of a successful registration.
type Response struct {
	UserID string `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"email_taken"`
	Message   string `json:"message" example:"email is already registered"`
}

// Register creates a new user account.
// @Summary      Register a new user
// @Description  Creates a new user account with email and password. Password must be at least 8 characters.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Registration payload"
// @Success      201   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (missing_email, password_too_short)"
// @Failure      409   {object}  ErrorResponse  "email_taken"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Router       /auth/register [post]
func (h *Handler) Register(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "missing_email",
			Message:   "email is required",
		})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "password_too_short",
			Message:   "password must be at least 8 characters",
		})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			ErrorCode: "internal_error",
			Message:   "internal server error",
		})
		return
	}

	user := &auth.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		PasswordHash: string(hash),
		CreatedAt:    time.Now(),
	}

	if err := h.store.Create(user); err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			c.JSON(http.StatusConflict, ErrorResponse{
				ErrorCode: "email_taken",
				Message:   err.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			ErrorCode: "internal_error",
			Message:   "internal server error",
		})
		return
	}

	c.JSON(http.StatusCreated, Response{UserID: user.ID})
}
