package getusermovements

import (
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
)

// UserMovementView is the enriched read model for a movement across all user wallets.
type UserMovementView struct {
	ID         string    `json:"id" example:"880e8400-e29b-41d4-a716-446655440000"`
	WalletID   string    `json:"wallet_id" example:"550e8400-e29b-41d4-a716-446655440000"`
	WalletName string    `json:"wallet_name" example:"Savings"`
	Type       string    `json:"type" example:"deposit" enums:"deposit,withdrawal,transfer_in,transfer_out"`
	Amount     int       `json:"amount" example:"5000"`
	Currency   string    `json:"currency" example:"EUR"`
	Reason     string    `json:"reason" example:"Salary"`
	Category   string    `json:"category" example:"food"`
	OccurredAt time.Time `json:"occurred_at" example:"2026-04-26T12:30:00Z"`
}

type Handler struct {
	walletStore   projections.WalletStore
	movementStore projections.MovementStore
}

func NewHandler(walletStore projections.WalletStore, movementStore projections.MovementStore) *Handler {
	return &Handler{
		walletStore:   walletStore,
		movementStore: movementStore,
	}
}

// Response contains the list of movements across all user wallets.
type Response struct {
	Movements []UserMovementView `json:"movements"`
}

// GetUserMovements returns all movements across all wallets for the authenticated user.
// @Summary      List user movements
// @Description  Returns all movements (deposits, withdrawals, transfers in/out) across all wallets for the authenticated user, sorted by most recent first.
// @Tags         Movements
// @Produce      json
// @Success      200 {object}  Response
// @Failure      401 {object}  object  "unauthorized"
// @Security     BearerAuth
// @Router       /movements [get]
func (h *Handler) GetUserMovements(c *gin.Context) {
	ctx := c.Request.Context()
	userID := middleware.GetUserID(ctx)

	wallets, err := h.walletStore.ListByUserID(ctx, userID)
	if err != nil {
		slog.Error("failed to list wallets", "user_id", userID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list movements"})
		return
	}

	walletNames := make(map[string]string, len(wallets))
	for _, w := range wallets {
		walletNames[w.ID] = w.Name
	}

	var all []UserMovementView
	for _, w := range wallets {
		movements, err := h.movementStore.ListByWalletID(ctx, w.ID)
		if err != nil {
			slog.Error("failed to list movements", "wallet_id", w.ID, "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error_code": "internal_error", "message": "failed to list movements"})
			return
		}
		for _, m := range movements {
			all = append(all, UserMovementView{
				ID:         m.ID,
				WalletID:   m.WalletID,
				WalletName: walletNames[m.WalletID],
				Type:       m.Type,
				Amount:     m.Amount,
				Currency:   m.Currency,
				Reason:     m.Reason,
				Category:   m.Category,
				OccurredAt: m.OccurredAt,
			})
		}
	}

	sort.Slice(all, func(i, j int) bool {
		return all[i].OccurredAt.After(all[j].OccurredAt)
	})

	if all == nil {
		all = []UserMovementView{}
	}

	c.JSON(http.StatusOK, Response{Movements: all})
}
