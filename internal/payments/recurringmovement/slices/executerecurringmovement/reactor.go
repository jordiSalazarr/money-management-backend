package executerecurringmovement

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// ReactorFunc is the typed reactor signature. The reactor Lambda's router
// decodes a Kinesis record into the typed event + metadata before
// dispatching. meta.EventID feeds the wallet command's IdempotencyKey so
// retries don't double-move money.
type ReactorFunc func(ctx context.Context, e recurringmovement.RecurringMovementExecuted, meta eventstore.EventMetadata) error

// NewReactorHandler builds a ReactorFunc that turns a
// RecurringMovementExecuted event into a wallet deposit or withdrawal.
// It loads the recurring-movement aggregate to discover the wallet,
// amount, currency, and direction (fields the event itself doesn't
// carry).
func NewReactorHandler(store eventstore.Store) ReactorFunc {
	depositHandler := depositmoney.NewCommandHandler(store)
	withdrawHandler := withdrawmoney.NewCommandHandler(store)

	return func(ctx context.Context, e recurringmovement.RecurringMovementExecuted, meta eventstore.EventMetadata) error {
		agg, err := loadRecurringAggregate(ctx, store, e.RecurringMovementID)
		if err != nil {
			return fmt.Errorf("loading recurring %s: %w", e.RecurringMovementID, err)
		}

		switch agg.Type() {
		case "deposit":
			_, err := depositHandler.Handle(ctx, depositmoney.Command{
				WalletID:       agg.WalletID(),
				UserID:         agg.UserID(),
				Amount:         agg.Amount(),
				Currency:       agg.Currency(),
				Reason:         agg.Reason(),
				Category:       "Other",
				IdempotencyKey: meta.EventID,
			})
			if err != nil {
				return fmt.Errorf("depositing for recurring %s: %w", e.RecurringMovementID, err)
			}
			slog.Info("recurring movement: deposited",
				"recurring_movement_id", e.RecurringMovementID,
				"wallet_id", agg.WalletID(),
				"amount", agg.Amount(),
				"source_event_id", meta.EventID,
			)
		case "withdrawal":
			_, err := withdrawHandler.Handle(ctx, withdrawmoney.Command{
				WalletID:       agg.WalletID(),
				UserID:         agg.UserID(),
				Amount:         agg.Amount(),
				Currency:       agg.Currency(),
				Reason:         agg.Reason(),
				Category:       "Other",
				IdempotencyKey: meta.EventID,
			})
			if err != nil {
				return fmt.Errorf("withdrawing for recurring %s: %w", e.RecurringMovementID, err)
			}
			slog.Info("recurring movement: withdrawn",
				"recurring_movement_id", e.RecurringMovementID,
				"wallet_id", agg.WalletID(),
				"amount", agg.Amount(),
				"source_event_id", meta.EventID,
			)
		default:
			return fmt.Errorf("recurring %s has unknown type %q", e.RecurringMovementID, agg.Type())
		}

		return nil
	}
}

func loadRecurringAggregate(ctx context.Context, store eventstore.Store, id string) (*recurringmovement.Aggregate, error) {
	events, err := store.Load(ctx, id)
	if err != nil {
		return nil, err
	}
	agg := recurringmovement.NewAggregate()
	for _, e := range events {
		raw, err := unmarshalRecurringEventForReactor(e)
		if err != nil {
			return nil, err
		}
		if raw != nil {
			agg.Apply(raw)
		}
	}
	return agg, nil
}

func unmarshalRecurringEventForReactor(e eventstore.Event) (any, error) {
	switch e.EventType {
	case recurringmovement.EventTypeRecurringMovementRegistered:
		var evt recurringmovement.RecurringMovementRegistered
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case recurringmovement.EventTypeRecurringMovementExecuted:
		var evt recurringmovement.RecurringMovementExecuted
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	case recurringmovement.EventTypeRecurringMovementStopped:
		var evt recurringmovement.RecurringMovementStopped
		if err := json.Unmarshal(e.Payload, &evt); err != nil {
			return nil, err
		}
		return evt, nil
	default:
		return nil, nil
	}
}
