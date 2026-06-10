package voice

import (
	"context"

	"github.com/gin-gonic/gin"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// RegisterRoutes wires the voice command endpoints.
//
// Two endpoints live under /voice: /command for typed text (no STT
// round trip) and /audio for spoken commands (multipart WAV upload
// transcribed by AWS Transcribe). Both then funnel through the same
// interpret+execute pipeline.
//
// Both backends can fail to construct (AWS config load, etc.) so the
// function returns an error — callers gate route mounting on success.
func RegisterRoutes(
	ctx context.Context,
	router *gin.Engine,
	store eventstore.Store,
	opts ClientOptions,
	transOpts TranscriberOptions,
	walletStore wprojections.WalletStore,
	debtStore dprojections.DebtStore,
	recurringStore rmprojections.RecurringMovementStore,
) error {
	client, err := NewBackend(ctx, opts)
	if err != nil {
		return err
	}
	transcriber, err := NewTranscriber(ctx, transOpts)
	if err != nil {
		return err
	}
	resolver := NewResolver(walletStore, debtStore, recurringStore)
	executor := NewExecutor(store, resolver)
	handler := NewHandler(client, transcriber, executor, resolver)

	router.POST("/voice/command", handler.HandleVoiceCommand)
	router.POST("/voice/audio", handler.HandleVoiceAudio)
	return nil
}
