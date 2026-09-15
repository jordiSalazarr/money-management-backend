package occurrence

import (
	"context"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/rejection"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

type Reactor struct {
	store    eventstore.Store
	codec    *eventstore.Codec
	deposit  *depositmoney.CommandHandler
	withdraw *withdrawmoney.CommandHandler
	now      func() time.Time
}

func NewReactor(store eventstore.Store, codec *eventstore.Codec, now func() time.Time) *Reactor {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reactor{
		store:    store,
		codec:    codec,
		deposit:  depositmoney.NewCommandHandler(store, codec),
		withdraw: withdrawmoney.NewCommandHandler(store, codec),
		now:      now,
	}
}

// movementKey is derived from the occurrence, which is itself derived from the
// recurring movement and the date. Two cron runs for the same day therefore
// produce the same key and the wallet applies the movement once.
func movementKey(occurrenceID string) string { return "occurrence:" + occurrenceID }

func (r *Reactor) Handle(ctx context.Context, event any, _ eventstore.EventMetadata) error {
	e, ok := event.(occurrence.MovementDue)
	if !ok {
		return nil
	}
	return r.fire(ctx, e.RecurringMovementID, e.ForDate)
}

func (r *Reactor) fire(ctx context.Context, recurringMovementID string, forDate time.Time) error {
	stream := streams.Occurrence(recurringMovementID, forDate)

	agg, expected, err := r.load(ctx, stream.ID)
	if err != nil {
		return err
	}
	if agg.IsTerminal() {
		return nil // a redelivery of a date that already settled
	}

	cmdErr := r.move(ctx, agg)

	switch {
	case cmdErr == nil, rejection.IsAlreadyProcessed(cmdErr):
		if err := agg.Execute(r.now()); err != nil {
			return nil
		}
	default:
		code, permanent := rejection.Classify(cmdErr)
		if !permanent {
			return fmt.Errorf("firing occurrence %s: %w", stream, cmdErr)
		}
		// The wallet refused. Record the skip — this is a reportable outcome a
		// person is entitled to see, not an error to swallow.
		if err := agg.Skip(code, r.now()); err != nil {
			return nil
		}
	}

	return r.appendTo(ctx, stream.ID, expected, agg)
}

func (r *Reactor) move(ctx context.Context, agg *occurrence.Aggregate) error {
	switch agg.Type() {
	case "deposit":
		_, err := r.deposit.Handle(ctx, depositmoney.Command{
			WalletID: agg.WalletID(), UserID: agg.UserID(),
			Amount: agg.Amount(), Currency: agg.Currency(),
			Reason: agg.Reason(), Category: "Other",
			IdempotencyKey: movementKey(agg.ID()),
		})
		return err
	case "withdrawal":
		_, err := r.withdraw.Handle(ctx, withdrawmoney.Command{
			WalletID: agg.WalletID(), UserID: agg.UserID(),
			Amount: agg.Amount(), Currency: agg.Currency(),
			Reason: agg.Reason(), Category: "Other",
			IdempotencyKey: movementKey(agg.ID()),
		})
		return err
	default:
		return fmt.Errorf("occurrence %s has unknown type %q", agg.ID(), agg.Type())
	}
}

// Redrive continues an occurrence from its recorded state.
func (r *Reactor) Redrive(ctx context.Context, occurrenceID string) error {
	agg, _, err := r.load(ctx, occurrenceID)
	if err != nil {
		return err
	}
	if agg.IsTerminal() {
		return nil
	}
	return r.fire(ctx, agg.RecurringMovementID(), agg.ForDate())
}

// GiveUp skips an occurrence the sweeper has stopped re-driving.
//
// Unlike a transfer, an occurrence that has not executed has moved no money at
// all — there is no partial state to strand — so giving up is always a skip,
// never a stuck. The schedule still advances, so one bad month does not jam
// the standing order.
func (r *Reactor) GiveUp(ctx context.Context, occurrenceID string) error {
	agg, expected, err := r.load(ctx, occurrenceID)
	if err != nil {
		return err
	}
	if agg.IsTerminal() {
		return nil
	}
	if err := agg.Skip(rejection.ReasonTimeout, r.now()); err != nil {
		return nil
	}
	return r.appendTo(ctx, occurrenceID, expected, agg)
}

func (r *Reactor) RecordAttempt(ctx context.Context, occurrenceID string) error {
	agg, expected, err := r.load(ctx, occurrenceID)
	if err != nil {
		return err
	}
	if err := agg.RecordAttempt(r.now()); err != nil {
		return nil
	}
	return r.appendTo(ctx, occurrenceID, expected, agg)
}

func (r *Reactor) load(ctx context.Context, occurrenceID string) (*occurrence.Aggregate, int, error) {
	agg := occurrence.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, r.store, r.codec, streams.OccurrenceByID(occurrenceID), agg.Apply)
	if err != nil {
		return nil, 0, fmt.Errorf("loading occurrence %s: %w", occurrenceID, err)
	}
	if expected == eventstore.NoStream {
		return nil, 0, fmt.Errorf("loading occurrence %s: %w", occurrenceID, eventstore.ErrAggregateNotFound)
	}
	return agg, expected, nil
}

func (r *Reactor) appendTo(ctx context.Context, occurrenceID string, expected int, agg *occurrence.Aggregate) error {
	err := eventstore.AppendChanges(ctx, r.store, r.codec, streams.OccurrenceByID(occurrenceID),
		expected, agg.Changes(), "", r.now())
	if err != nil {
		return fmt.Errorf("recording occurrence %s: %w", occurrenceID, err)
	}
	return nil
}
