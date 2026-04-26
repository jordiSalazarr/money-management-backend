package addwallet

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/wallet"
)

type Handler struct {
	cmdHandler *CommandHandler
}

func NewHandler(cmdHandler *CommandHandler) *Handler {
	return &Handler{cmdHandler: cmdHandler}
}

// Request is the payload for creating a new wallet.
type Request struct {
	Name          string `json:"name" example:"Savings"`
	Description   string `json:"description" example:"My savings wallet"`
	InitialAmount int    `json:"initial_amount" example:"10000"`
	Currency      string `json:"currency" example:"EUR"`
	Category      string `json:"category" example:"savings"`
}

// Response is the result of creating a wallet.
type Response struct {
	WalletID string `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
}

// ErrorResponse is the standard error shape.
type ErrorResponse struct {
	ErrorCode string `json:"error_code" example:"missing_name"`
	Message   string `json:"message" example:"name is required"`
}

// AddWallet creates a new wallet for a user.
// @Summary      Create a wallet
// @Description  Creates a new wallet with an initial balance. All amounts are in integer cents.
// @Tags         Wallets
// @Accept       json
// @Produce      json
// @Param        body  body      Request  true  "Wallet creation payload"
// @Success      201   {object}  Response
// @Failure      400   {object}  ErrorResponse  "Validation error (missing_name, invalid_initial_amount, invalid_currency, invalid_category)"
// @Failure      401   {object}  ErrorResponse  "unauthorized"
// @Failure      409   {object}  ErrorResponse  "max_wallets_reached"
// @Failure      500   {object}  ErrorResponse  "internal_error"
// @Security     BearerAuth
// @Router       /wallets [post]
func (h *Handler) AddWallet(c *gin.Context) {
	var req Request
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, ErrorResponse{
			ErrorCode: "invalid_request",
			Message:   "invalid JSON body",
		})
		return
	}

	cmd := Command{
		UserID:        middleware.GetUserID(c.Request.Context()),
		Name:          req.Name,
		Description:   req.Description,
		InitialAmount: req.InitialAmount,
		Currency:      req.Currency,
		Category:      req.Category,
	}

	walletID, err := h.cmdHandler.Handle(c.Request.Context(), cmd)
	if err != nil {
		status, errResp := mapError(err)
		c.JSON(status, errResp)
		return
	}

	c.JSON(http.StatusCreated, Response{WalletID: walletID})
}

func mapError(err error) (int, ErrorResponse) {
	switch {
	case errors.Is(err, wallet.ErrMissingUserID):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_user_id", Message: err.Error()}
	case errors.Is(err, wallet.ErrMissingName):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "missing_name", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidInitialAmount):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_initial_amount", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidCurrency):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_currency", Message: err.Error()}
	case errors.Is(err, wallet.ErrInvalidCategory):
		return http.StatusBadRequest, ErrorResponse{ErrorCode: "invalid_category", Message: err.Error()}
	case errors.Is(err, ErrMaxWalletsReached):
		return http.StatusConflict, ErrorResponse{ErrorCode: "max_wallets_reached", Message: err.Error()}
	default:
		return http.StatusInternalServerError, ErrorResponse{ErrorCode: "internal_error", Message: "internal server error"}
	}
}
