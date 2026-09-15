// Package debtsettlement carries a requested debt settlement through to the
// wallet movement that makes it true.
//
// Before this existed, settledebt appended DebtSettled and *then* asked the
// reactor to move the money. When the wallet refused — an ordinary
// insufficient balance was enough — the error was classified permanent and the
// record was dropped with a log.Warn. The debt read settled; nothing had been
// paid.
package debtsettlement

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/rejection"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
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

// settlementKey is derived from the debt rather than the envelope id, so it
// stays stable if the debt stream is ever rebuilt.
func settlementKey(debtID string) string { return "debt:" + debtID + ":settle" }

func (r *Reactor) Handle(ctx context.Context, event any, meta eventstore.EventMetadata) error {
	// Carry the triggering event's correlation id onto everything this step
	// writes, so one request can be followed across all the streams a process
	// touches. CLAUDE.md requires it to flow command -> events -> projections;
	// a saga is where that matters most and where it used to stop.
	ctx = middleware.WithCorrelationID(ctx, meta.CorrelationID)
	e, ok := event.(debt.DebtSettlementRequested)
	if !ok {
		return nil
	}
	return r.settle(ctx, e.DebtID, e.RequestedBy)
}

func (r *Reactor) settle(ctx context.Context, debtID, requestedBy string) error {
	agg, expected, err := r.load(ctx, debtID)
	if err != nil {
		return err
	}
	// Already settled on an earlier delivery, or no settlement in progress.
	if agg.Status() != debt.DebtStatusSettling {
		return nil
	}

	cmdErr := r.move(ctx, agg, requestedBy)

	switch {
	case cmdErr == nil, rejection.IsAlreadyProcessed(cmdErr):
		if err := agg.MarkSettled(requestedBy, r.now()); err != nil {
			// A concurrent delivery got there first — the effect we wanted is
			// already in place, so this is a success, not a fault.
			if errors.Is(err, debt.ErrDebtAlreadySettled) || errors.Is(err, debt.ErrSettlementNotRequested) {
				return nil
			}
			return fmt.Errorf("settling debt %s: %w", debtID, err)
		}
	default:
		code, permanent := rejection.Classify(cmdErr)
		if !permanent {
			// Transient. Nothing was written, so the debt stays "settling"
			// and redelivery picks it up unchanged.
			return fmt.Errorf("settling debt %s: %w", debtID, cmdErr)
		}
		// The wallet refused on a rule. Record it — this is the whole point.
		// The debt goes back to standing and can be settled again later.
		if err := agg.FailSettlement(code, r.now()); err != nil {
			// A concurrent delivery already settled this. Nothing to do.
			if errors.Is(err, debt.ErrSettlementNotRequested) {
				return nil
			}
			return fmt.Errorf("recording failed settlement of debt %s: %w", debtID, err)
		}
	}

	return r.append(ctx, debtID, expected, agg)
}

// move applies the debt to the wallet. Direction "IN" means the money is owed
// to the wallet's owner, so settling it pays money in; "OUT" means they owed
// it, so settling pays money out.
func (r *Reactor) move(ctx context.Context, agg *debt.Aggregate, requestedBy string) error {
	reason := fmt.Sprintf("debt_settlement:%s", agg.ID())

	switch agg.Direction() {
	case "IN":
		_, err := r.deposit.Handle(ctx, depositmoney.Command{
			WalletID: agg.WalletID(), UserID: requestedBy,
			Amount: agg.Amount(), Currency: agg.Currency(),
			Reason: reason, Category: "Refund",
			IdempotencyKey: settlementKey(agg.ID()),
		})
		return err
	case "OUT":
		_, err := r.withdraw.Handle(ctx, withdrawmoney.Command{
			WalletID: agg.WalletID(), UserID: requestedBy,
			Amount: agg.Amount(), Currency: agg.Currency(),
			Reason: reason, Category: "Other",
			IdempotencyKey: settlementKey(agg.ID()),
		})
		return err
	default:
		return fmt.Errorf("debt %s has unknown direction %q", agg.ID(), agg.Direction())
	}
}

// Redrive continues a debt settlement from its recorded state.
func (r *Reactor) Redrive(ctx context.Context, debtID string) error {
	agg, _, err := r.load(ctx, debtID)
	if err != nil {
		return err
	}
	if agg.Status() != debt.DebtStatusSettling {
		return nil
	}
	return r.settle(ctx, debtID, agg.OwnerID())
}

// GiveUp fails a settlement the sweeper has stopped re-driving.
//
// A settlement that never completed moved no money, so the debt simply goes
// back to standing and can be settled again later. There is no stuck state to
// reach: the wallet movement and the debt's own status are the only two things
// involved, and the movement either happened or it did not.
func (r *Reactor) GiveUp(ctx context.Context, debtID string) error {
	agg, expected, err := r.load(ctx, debtID)
	if err != nil {
		return err
	}
	if agg.Status() != debt.DebtStatusSettling {
		return nil
	}
	if err := agg.FailSettlement(rejection.ReasonTimeout, r.now()); err != nil {
		if errors.Is(err, debt.ErrSettlementNotRequested) {
			return nil
		}
		return fmt.Errorf("giving up on debt %s: %w", debtID, err)
	}
	return r.append(ctx, debtID, expected, agg)
}

// RecordAttempt notes a sweeper re-drive on the debt's own stream.
//
// It has to be real. The sweeper spends its attempt budget by reading the
// count back off the process, so a no-op here would leave the budget at zero
// forever and the settlement would be re-driven on every single sweep.
func (r *Reactor) RecordAttempt(ctx context.Context, debtID string) error {
	agg, expected, err := r.load(ctx, debtID)
	if err != nil {
		return err
	}
	if err := agg.RecordSettlementAttempt(r.now()); err != nil {
		return nil // nothing in flight to count against
	}
	return r.append(ctx, debtID, expected, agg)
}

func (r *Reactor) load(ctx context.Context, debtID string) (*debt.Aggregate, int, error) {
	agg := debt.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, r.store, r.codec, streams.Debt(debtID), agg.Apply)
	if err != nil {
		return nil, 0, fmt.Errorf("loading debt %s: %w", debtID, err)
	}
	if expected == eventstore.NoStream {
		return nil, 0, fmt.Errorf("loading debt %s: %w", debtID, eventstore.ErrAggregateNotFound)
	}
	return agg, expected, nil
}

func (r *Reactor) append(ctx context.Context, debtID string, expected int, agg *debt.Aggregate) error {
	err := eventstore.AppendChanges(ctx, r.store, r.codec, streams.Debt(debtID),
		expected, agg.Changes(), middleware.GetCorrelationID(ctx), r.now())
	if err != nil {
		return fmt.Errorf("recording settlement of debt %s: %w", debtID, err)
	}
	return nil
}
