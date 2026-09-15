// Package transfer wires the transfer process: the reactor that carries a
// requested transfer through its two legs, and the slices that open one and
// report on it.
//
// This package is allowed to import wallet command slices. That is the
// documented exception in ADR 0002 — a process manager's job is to issue
// commands against aggregates it does not own. The arrow only points this way:
// no slice may import a process.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/rejection"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/streams"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/refundmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Reactor drives the transfer saga.
type Reactor struct {
	store    eventstore.Store
	codec    *eventstore.Codec
	withdraw *withdrawmoney.CommandHandler
	deposit  *depositmoney.CommandHandler
	refund   *refundmoney.CommandHandler
	now      func() time.Time
}

func NewReactor(store eventstore.Store, codec *eventstore.Codec, now func() time.Time) *Reactor {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Reactor{
		store:    store,
		codec:    codec,
		withdraw: withdrawmoney.NewCommandHandler(store, codec),
		deposit:  depositmoney.NewCommandHandler(store, codec),
		refund:   refundmoney.NewCommandHandler(store, codec),
		now:      now,
	}
}

// Idempotency keys are derived from the transfer id and the leg name rather
// than from the envelope's event id. Both are stable across redelivery, but a
// domain-derived key is also stable if the process stream is ever rebuilt or
// an event re-emitted with a new envelope id.
func debitKey(id string) string  { return id + ":debit" }
func creditKey(id string) string { return id + ":credit" }
func refundKey(id string) string { return id + ":refund" }

// Handle routes one decoded event. Event types this process does not care
// about are ignored — that is normal fan-out, not a fault.
func (r *Reactor) Handle(ctx context.Context, event any, _ eventstore.EventMetadata) error {
	switch e := event.(type) {
	case transfer.TransferRequested:
		return r.debit(ctx, e.TransferID)
	case transfer.TransferDebited:
		return r.credit(ctx, e.TransferID)
	default:
		return nil
	}
}

// debit runs the first leg: take the money out of the source wallet.
func (r *Reactor) debit(ctx context.Context, transferID string) error {
	agg, expected, err := r.load(ctx, transferID)
	if err != nil {
		return err
	}
	// Already settled, or already past this step. A redelivery, not a fault.
	if agg.IsTerminal() || agg.MoneyMoved() {
		return nil
	}

	_, cmdErr := r.withdraw.Handle(ctx, withdrawmoney.Command{
		WalletID:       agg.FromWalletID(),
		UserID:         agg.UserID(),
		Amount:         agg.Amount(),
		Currency:       agg.Currency(),
		Reason:         agg.Reason(),
		Category:       "Other",
		IdempotencyKey: debitKey(transferID),
	})

	switch {
	case cmdErr == nil, rejection.IsAlreadyProcessed(cmdErr):
		// Already-processed means the withdrawal landed on an earlier delivery
		// whose TransferDebited never got written. Recording it now is the
		// repair, not a duplicate.
		if err := agg.MarkDebited(r.now()); err != nil {
			return r.reconcile(ctx, transferID, err)
		}
	default:
		code, permanent := rejection.Classify(cmdErr)
		if !permanent {
			// Transient: let the subscription redeliver. Nothing was written,
			// so the transfer stays exactly where it was.
			return fmt.Errorf("debiting %s for transfer %s: %w", agg.FromWalletID(), transferID, cmdErr)
		}
		// The source wallet refused on a rule. No money moved, so this is a
		// clean failure with nothing to compensate.
		if err := agg.Fail(code, r.now()); err != nil {
			return r.reconcile(ctx, transferID, err)
		}
	}

	return r.append(ctx, transferID, expected, agg)
}

// credit runs the second leg: put the money into the destination wallet, and
// compensate if that is refused.
func (r *Reactor) credit(ctx context.Context, transferID string) error {
	agg, expected, err := r.load(ctx, transferID)
	if err != nil {
		return err
	}
	if agg.IsTerminal() {
		return nil
	}

	_, cmdErr := r.deposit.Handle(ctx, depositmoney.Command{
		WalletID:       agg.ToWalletID(),
		UserID:         agg.UserID(),
		Amount:         agg.Amount(),
		Currency:       agg.Currency(),
		Reason:         agg.Reason(),
		Category:       "Transfer In",
		IdempotencyKey: creditKey(transferID),
	})

	switch {
	case cmdErr == nil, rejection.IsAlreadyProcessed(cmdErr):
		if err := agg.Complete(r.now()); err != nil {
			return r.reconcile(ctx, transferID, err)
		}
	default:
		code, permanent := rejection.Classify(cmdErr)
		if !permanent {
			return fmt.Errorf("crediting %s for transfer %s: %w", agg.ToWalletID(), transferID, cmdErr)
		}
		// The money is already out of the source. Put it back before failing.
		if err := r.compensate(ctx, agg, transferID, code); err != nil {
			return err
		}
	}

	return r.append(ctx, transferID, expected, agg)
}

// compensate returns the debited money to the source wallet and settles the
// transfer.
//
// The refund goes through wallet.Refund rather than a deposit precisely so the
// common failure cannot happen: a user disabling the source wallet between the
// debit and the compensation used to leave the funds stranded with nothing but
// a log line to show for it.
//
// If the refund is refused anyway, the transfer is marked stuck. In the
// current domain that branch is not reachable — Refund only rejects on a
// non-positive amount, a currency the withdrawal would already have caught, or
// an idempotency key that means it has already been refunded. It stays because
// the set of reasons a compensation can fail is not knowable in advance, and
// the one thing that must never happen is money disappearing silently.
func (r *Reactor) compensate(ctx context.Context, agg *transfer.Aggregate, transferID, failReason string) error {
	refundErr := r.refund.Handle(ctx, refundmoney.Command{
		WalletID:       agg.FromWalletID(),
		UserID:         agg.UserID(),
		Amount:         agg.Amount(),
		Currency:       agg.Currency(),
		Reason:         "transfer " + transferID + " could not be credited",
		IdempotencyKey: refundKey(transferID),
	}, r.now())

	switch {
	case refundErr == nil, rejection.IsAlreadyProcessed(refundErr):
		// The wallets are whole again, so this is a failure, not a loss.
		return agg.Fail(failReason, r.now())
	default:
		if _, permanent := rejection.Classify(refundErr); !permanent {
			return fmt.Errorf("refunding %s for transfer %s: %w", agg.FromWalletID(), transferID, refundErr)
		}
		return agg.MarkStuck("refund_rejected", r.now())
	}
}

func (r *Reactor) load(ctx context.Context, transferID string) (*transfer.Aggregate, int, error) {
	agg := transfer.NewAggregate()
	expected, err := eventstore.Rehydrate(ctx, r.store, r.codec, streams.Transfer(transferID), agg.Apply)
	if err != nil {
		return nil, 0, fmt.Errorf("loading transfer %s: %w", transferID, err)
	}
	if expected == eventstore.NoStream {
		return nil, 0, fmt.Errorf("loading transfer %s: %w", transferID, eventstore.ErrAggregateNotFound)
	}
	return agg, expected, nil
}

func (r *Reactor) append(ctx context.Context, transferID string, expected int, agg *transfer.Aggregate) error {
	err := eventstore.AppendChanges(ctx, r.store, r.codec, streams.Transfer(transferID),
		expected, agg.Changes(), "", r.now())
	if err != nil {
		return fmt.Errorf("recording progress on transfer %s: %w", transferID, err)
	}
	return nil
}

// reconcile turns a state-machine refusal into either "already done, stop" or
// a real error.
//
// A process aggregate rejecting a transition usually means a concurrent
// delivery got there first. Retrying would conflict forever, so the right
// answer is to stop — the work is done.
func (r *Reactor) reconcile(_ context.Context, transferID string, err error) error {
	switch {
	case err == nil:
		return nil
	case isAlreadyDone(err):
		return nil
	default:
		return fmt.Errorf("transfer %s: %w", transferID, err)
	}
}

func isAlreadyDone(err error) bool {
	return errors.Is(err, transfer.ErrAlreadyDebited) || errors.Is(err, transfer.ErrAlreadyTerminal)
}

// Redrive continues a transfer from whatever state it is recorded in. The
// sweeper calls it for transfers that have stopped making progress — because
// the reactor was down, or a message was parked, or an event was never routed.
//
// It is safe to call at any time: each step carries a stable idempotency key,
// so re-running one that already succeeded is a no-op.
func (r *Reactor) Redrive(ctx context.Context, transferID string) error {
	agg, _, err := r.load(ctx, transferID)
	if err != nil {
		return err
	}
	switch agg.Status() {
	case transfer.StatusRequested:
		return r.debit(ctx, transferID)
	case transfer.StatusDebited:
		return r.credit(ctx, transferID)
	default:
		return nil
	}
}

// GiveUp settles a transfer the sweeper has stopped re-driving.
//
// What it writes depends on whether money moved, which the stream answers
// exactly: nothing moved means the wallets are whole and this is a clean
// failure; money moved means it is out there and the honest outcome is stuck.
func (r *Reactor) GiveUp(ctx context.Context, transferID string) error {
	agg, expected, err := r.load(ctx, transferID)
	if err != nil {
		return err
	}
	if agg.IsTerminal() {
		return nil
	}

	if agg.MoneyMoved() {
		err = agg.MarkStuck(rejection.ReasonTimeout, r.now())
	} else {
		err = agg.Fail(rejection.ReasonTimeout, r.now())
	}
	if err != nil {
		return nil
	}
	return r.append(ctx, transferID, expected, agg)
}

// RecordAttempt notes a sweeper re-drive on the transfer's own stream, so the
// count survives a sweeper restart.
func (r *Reactor) RecordAttempt(ctx context.Context, transferID string) error {
	agg, expected, err := r.load(ctx, transferID)
	if err != nil {
		return err
	}
	if err := agg.RecordAttempt(r.now()); err != nil {
		return nil
	}
	return r.append(ctx, transferID, expected, agg)
}
