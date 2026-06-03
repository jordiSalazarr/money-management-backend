package executerecurringmovement

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/outbox"
	"github.com/jordisalazar/money-management/internal/payments/wallet/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/withdrawmoney"
)

// RecurringMovementExecutedPayload is the outbox payload for wallet reactions.
type RecurringMovementExecutedPayload struct {
	RecurringMovementID string `json:"recurring_movement_id"`
	WalletID            string `json:"wallet_id"`
	UserID              string `json:"user_id"`
	Type                string `json:"type"`
	Amount              int    `json:"amount"`
	Currency            string `json:"currency"`
	Reason              string `json:"reason"`
}

// NewReactorHandler returns an outbox.Handler that dispatches wallet
// deposit/withdrawal commands when a RecurringMovementExecuted outbox entry is processed.
func NewReactorHandler(store eventstore.Store) outbox.Handler {
	depositHandler := depositmoney.NewCommandHandler(store)
	withdrawHandler := withdrawmoney.NewCommandHandler(store)

	return func(ctx context.Context, entry outbox.Entry) error {
		if entry.EventType != recurringmovement.EventTypeRecurringMovementExecuted {
			return fmt.Errorf("unexpected event type: %s", entry.EventType)
		}

		var payload RecurringMovementExecutedPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshalling payload: %w", err)
		}

		switch payload.Type {
		case "deposit":
			_, err := depositHandler.Handle(ctx, depositmoney.Command{
				WalletID: payload.WalletID,
				UserID:   payload.UserID,
				Amount:   payload.Amount,
				Currency: payload.Currency,
				Reason:   payload.Reason,
				Category: "Other",
			})
			if err != nil {
				return fmt.Errorf("depositing for recurring movement %s: %w", payload.RecurringMovementID, err)
			}
			slog.Info("recurring movement: deposited",
				"recurring_movement_id", payload.RecurringMovementID,
				"wallet_id", payload.WalletID,
				"amount", payload.Amount,
			)
		case "withdrawal":
			_, err := withdrawHandler.Handle(ctx, withdrawmoney.Command{
				WalletID: payload.WalletID,
				UserID:   payload.UserID,
				Amount:   payload.Amount,
				Currency: payload.Currency,
				Reason:   payload.Reason,
				Category: "Other",
			})
			if err != nil {
				return fmt.Errorf("withdrawing for recurring movement %s: %w", payload.RecurringMovementID, err)
			}
			slog.Info("recurring movement: withdrawn",
				"recurring_movement_id", payload.RecurringMovementID,
				"wallet_id", payload.WalletID,
				"amount", payload.Amount,
			)
		default:
			return fmt.Errorf("unknown type: %s", payload.Type)
		}

		return nil
	}
}
