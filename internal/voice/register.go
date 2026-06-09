package voice

import (
	"context"

	"github.com/gin-gonic/gin"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// RegisterRoutes wires the voice command endpoint.
//
// Phase 4b: takes ClientOptions instead of an API key string so the
// caller (internal/app/engine.go) can pick between the Bedrock and
// direct-Anthropic backends. Building the backend can fail (e.g. AWS
// config load), so it returns an error — callers gate route mounting
// on success.
func RegisterRoutes(
	ctx context.Context,
	router *gin.Engine,
	store eventstore.Store,
	opts ClientOptions,
	walletStore wprojections.WalletStore,
	debtStore dprojections.DebtStore,
	recurringStore rmprojections.RecurringMovementStore,
) error {
	client, err := NewBackend(ctx, opts)
	if err != nil {
		return err
	}
	resolver := NewResolver(walletStore, debtStore, recurringStore)
	executor := NewExecutor(store, resolver)
	handler := NewHandler(client, executor, resolver)

	router.POST("/voice/command", handler.HandleVoiceCommand)
	return nil
}
