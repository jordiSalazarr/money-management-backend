package login

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/auth"
	"golang.org/x/crypto/bcrypt"
)

type Handler struct {
	store     auth.UserStorer
	jwtSecret string
}

func NewHandler(store auth.UserStorer, jwtSecret string) *Handler {
	return &Handler{store: store, jwtSecret: jwtSecret}
}

// Request is the payload for user login.
type Request struct {
	Email    string `json:"email" example:"user@example.com"`
	Password string `json:"password" example:"securepass123"`
}

// Response is the result of a successful login.
type Response struct {
	Token  string `json:"token" example:"eyJhbGciOiJIUzI1NiIs..."`
	UserID string `json:"user_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"invalid_credentials"`
	Message   string `json:"message" example:"invalid email or password"`
}

// Login authenticates a user and returns a JWT token.
// @Summary      Login
// @Description  Authenticates with email and password, returns a JWT bearer token valid for 24 hours.
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Login payload"
// @Success      200   {object}  Response
// @Failure      400   {object}  ErrorResponse  "invalid_request"
// @Failure      401   {object}  ErrorResponse  "invalid_credentials"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Router       /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	user, err := h.store.FindByEmail(req.Email)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			c.JSON(http.StatusUnauthorized, ErrorResponse{
				ErrorCode: "invalid_credentials",
				Message:   auth.ErrInvalidCredentials.Error(),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			ErrorCode: "internal_error",
			Message:   "internal server error",
		})
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, ErrorResponse{
			ErrorCode: "invalid_credentials",
			Message:   auth.ErrInvalidCredentials.Error(),
		})
		return
	}

	token, err := auth.GenerateToken(user.ID, h.jwtSecret, time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, ErrorResponse{
			ErrorCode: "internal_error",
			Message:   "internal server error",
		})
		return
	}

	c.JSON(http.StatusOK, Response{
		Token:  token,
		UserID: user.ID,
	})
}
