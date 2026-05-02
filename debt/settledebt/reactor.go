package settledebt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/debt"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/outbox"
	"github.com/jordisalazar/money-management/wallet/depositmoney"
	"github.com/jordisalazar/money-management/wallet/withdrawmoney"
)

// DebtSettledPayload is the outbox payload written when a debt is settled.
type DebtSettledPayload struct {
	DebtID    string `json:"debt_id"`
	WalletID  string `json:"wallet_id"`
	Amount    int    `json:"amount"`
	Currency  string `json:"currency"`
	Direction string `json:"direction"`
	SettledBy string `json:"settled_by"`
}

// NewReactorHandler returns an outbox.Handler that dispatches wallet
// deposit/withdrawal commands when a DebtSettled outbox entry is processed.
func NewReactorHandler(store eventstore.Store) outbox.Handler {
	depositHandler := depositmoney.NewCommandHandler(store)
	withdrawHandler := withdrawmoney.NewCommandHandler(store)

	return func(ctx context.Context, entry outbox.Entry) error {
		if entry.EventType != debt.EventTypeDebtSettled {
			return fmt.Errorf("unexpected event type: %s", entry.EventType)
		}

		var payload DebtSettledPayload
		if err := json.Unmarshal(entry.Payload, &payload); err != nil {
			return fmt.Errorf("unmarshalling payload: %w", err)
		}

		reason := fmt.Sprintf("debt_settlement:%s", payload.DebtID)

		switch payload.Direction {
		case "IN":
			_, err := depositHandler.Handle(ctx, depositmoney.Command{
				WalletID: payload.WalletID,
				UserID:   payload.SettledBy,
				Amount:   payload.Amount,
				Currency: payload.Currency,
				Reason:   reason,
				Category: "Refund",
			})
			if err != nil {
				return fmt.Errorf("depositing money for debt %s: %w", payload.DebtID, err)
			}
			slog.Info("debt settlement: deposited",
				"debt_id", payload.DebtID,
				"wallet_id", payload.WalletID,
				"amount", payload.Amount,
			)
		case "OUT":
			_, err := withdrawHandler.Handle(ctx, withdrawmoney.Command{
				WalletID: payload.WalletID,
				UserID:   payload.SettledBy,
				Amount:   payload.Amount,
				Currency: payload.Currency,
				Reason:   reason,
				Category: "Other",
			})
			if err != nil {
				return fmt.Errorf("withdrawing money for debt %s: %w", payload.DebtID, err)
			}
			slog.Info("debt settlement: withdrawn",
				"debt_id", payload.DebtID,
				"wallet_id", payload.WalletID,
				"amount", payload.Amount,
			)
		default:
			return fmt.Errorf("unknown direction: %s", payload.Direction)
		}

		return nil
	}
}
