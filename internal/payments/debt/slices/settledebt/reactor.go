package settledebt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Handler is the reactor signature: it takes the typed DebtSettled event
// plus the eventstore metadata. The reactor Lambda's router decodes
// Kinesis records into this shape and dispatches.
//
// meta.EventID is used as the wallet command's idempotency key, so a
// Kinesis retry doesn't issue the deposit/withdraw twice.
type ReactorFunc func(ctx context.Context, e debt.DebtSettled, meta eventstore.EventMetadata) error

// NewReactorHandler returns a Handler that turns a DebtSettled event into
// a wallet deposit or withdrawal, depending on the debt's direction. It
// loads the debt aggregate from the event store to get the wallet,
// amount, currency, and direction — fields the DebtSettled event itself
// does not carry.
func NewReactorHandler(store eventstore.Store) ReactorFunc {
	depositHandler := depositmoney.NewCommandHandler(store)
	withdrawHandler := withdrawmoney.NewCommandHandler(store)

	return func(ctx context.Context, e debt.DebtSettled, meta eventstore.EventMetadata) error {
		agg, err := loadDebtAggregate(ctx, store, e.DebtID)
		if err != nil {
			return fmt.Errorf("loading debt %s: %w", e.DebtID, err)
		}

		reason := fmt.Sprintf("debt_settlement:%s", agg.ID())

		switch agg.Direction() {
		case "IN":
			_, err := depositHandler.Handle(ctx, depositmoney.Command{
				WalletID:       agg.WalletID(),
				UserID:         e.SettledBy,
				Amount:         agg.Amount(),
				Currency:       agg.Currency(),
				Reason:         reason,
				Category:       "Refund",
				IdempotencyKey: meta.EventID,
			})
			if err != nil {
				return fmt.Errorf("depositing for debt %s: %w", e.DebtID, err)
			}
			slog.Info("debt settlement: deposited",
				"debt_id", e.DebtID,
				"wallet_id", agg.WalletID(),
				"amount", agg.Amount(),
				"source_event_id", meta.EventID,
			)
		case "OUT":
			_, err := withdrawHandler.Handle(ctx, withdrawmoney.Command{
				WalletID:       agg.WalletID(),
				UserID:         e.SettledBy,
				Amount:         agg.Amount(),
				Currency:       agg.Currency(),
				Reason:         reason,
				Category:       "Other",
				IdempotencyKey: meta.EventID,
			})
			if err != nil {
				return fmt.Errorf("withdrawing for debt %s: %w", e.DebtID, err)
			}
			slog.Info("debt settlement: withdrawn",
				"debt_id", e.DebtID,
				"wallet_id", agg.WalletID(),
				"amount", agg.Amount(),
				"source_event_id", meta.EventID,
			)
		default:
			return fmt.Errorf("debt %s has unknown direction %q", e.DebtID, agg.Direction())
		}

		return nil
	}
}

// loadDebtAggregate hydrates the debt aggregate by replaying its events.
// The reactor only reads the aggregate; no Append happens through it.
func loadDebtAggregate(ctx context.Context, store eventstore.Store, debtID string) (*debt.Aggregate, error) {
	events, err := store.Load(ctx, debtID)
	if err != nil {
		return nil, err
	}
	agg := debt.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalDebtEventForReactor(e)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			agg.Apply(raw)
		}
	}
	return agg, nil
}

func unmarshalDebtEventForReactor(e eventstore.Event) (any, error) {
	switch e.EventType {
	case debt.EventTypeDebtRecorded:
		var evt debt.DebtRecorded
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case debt.EventTypeDebtSettled:
		var evt debt.DebtSettled
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
