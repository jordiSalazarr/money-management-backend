package voice

import (
	"github.com/gin-gonic/gin"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
)

// RegisterRoutes wires the voice command endpoint.
func RegisterRoutes(
	router *gin.Engine,
	store eventstore.Store,
	apiKey string,
	walletStore wprojections.WalletStore,
	debtStore dprojections.DebtStore,
	recurringStore rmprojections.RecurringMovementStore,
) {
	client := NewClient(apiKey)
	resolver := NewResolver(walletStore, debtStore, recurringStore)
	executor := NewExecutor(store, resolver)
	handler := NewHandler(client, executor, resolver)

	router.POST("/voice/command", handler.HandleVoiceCommand)
}
