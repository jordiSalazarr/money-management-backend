// Package reactor decodes Kinesis records produced by the DynamoDB events
// table and dispatches them to typed reactor functions.
//
// Compared to the projector dispatcher in internal/projector, the reactor
// router knows that only a small subset of event types matter (the ones
// with a wallet-side side-effect), and that errors classify into
// permanent (drop the record) and retryable (let Kinesis retry the
// batch). The classifier lives here, not in the reactor functions
// themselves, so the rules stay in one place.
package reactor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/aws/aws-lambda-go/events"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/executerecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/projector/kinesisdecode"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Router wires typed reactor functions to event types and runs the
// decode + dispatch loop for a Kinesis batch.
type Router struct {
	log              *slog.Logger
	settledebt       settledebt.ReactorFunc
	recurringMovement executerecurringmovement.ReactorFunc
}

// NewRouter constructs a Router from the two typed reactor functions.
// Pass nil log to use slog.Default().
func NewRouter(log *slog.Logger, debtFn settledebt.ReactorFunc, recurringFn executerecurringmovement.ReactorFunc) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{
		log:              log,
		settledebt:       debtFn,
		recurringMovement: recurringFn,
	}
}

// HandleBatch processes a Kinesis batch in arrival order. Per-shard
// concurrency is configured to 1 at the event-source mapping, so this
// stays sequential.
func (r *Router) HandleBatch(ctx context.Context, recs []events.KinesisEventRecord) error {
	for _, rec := range recs {
		if err := r.dispatchOne(ctx, rec); err != nil {
			return fmt.Errorf("record %s: %w", rec.EventID, err)
		}
	}
	return nil
}

func (r *Router) dispatchOne(ctx context.Context, rec events.KinesisEventRecord) error {
	raw, err := kinesisdecode.Unmarshal(rec.Kinesis.Data)
	if err != nil {
		return fmt.Errorf("decoding kinesis payload: %w", err)
	}

	switch raw.EventName {
	case "INSERT", "MODIFY":
		// Carry on.
	case "REMOVE":
		// The events table is append-only by contract; a REMOVE record
		// means someone deleted from the console. Skip + warn.
		r.log.Warn("reactor: REMOVE record skipped — event store should be append-only",
			"event_id", rec.EventID)
		return nil
	default:
		r.log.Warn("reactor: unrecognised eventName skipped",
			"event_id", rec.EventID, "eventName", raw.EventName)
		return nil
	}

	img := raw.Dynamodb.NewImage
	if img == nil {
		return errors.New("missing dynamodb.NewImage")
	}
	eventType, _ := img.String("event_type")
	if eventType == "" {
		return errors.New("missing event_type attribute")
	}
	payload, _ := img.String("payload")
	sourceEventID, _ := img.String("event_id")
	meta := eventstore.EventMetadata{EventID: sourceEventID}

	var handlerErr error
	switch eventType {
	case debt.EventTypeDebtSettled:
		var e debt.DebtSettled
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return fmt.Errorf("unmarshalling DebtSettled payload: %w", err)
		}
		handlerErr = r.settledebt(ctx, e, meta)

	case recurringmovement.EventTypeRecurringMovementExecuted:
		var e recurringmovement.RecurringMovementExecuted
		if err := json.Unmarshal([]byte(payload), &e); err != nil {
			return fmt.Errorf("unmarshalling RecurringMovementExecuted payload: %w", err)
		}
		handlerErr = r.recurringMovement(ctx, e, meta)

	default:
		// Every other event type — wallet writes, debt recorded, etc. —
		// has no reactor side-effect today. Logged at debug so the noise
		// stays out of normal operation.
		r.log.Debug("reactor: unrouted event type",
			"event_id", sourceEventID, "event_type", eventType)
		return nil
	}

	return r.classify(handlerErr, sourceEventID, eventType)
}

// classify decides whether a handler error is permanent (drop the
// record, return nil so Kinesis advances) or retryable (return error so
// Kinesis retries the whole batch). The list of permanent errors is a
// small whitelist of wallet domain sentinel errors — anything else is
// assumed transient.
func (r *Router) classify(handlerErr error, sourceEventID, eventType string) error {
	if handlerErr == nil {
		return nil
	}
	if isPermanent(handlerErr) {
		r.log.Warn("reactor: permanent error, dropping record",
			"event_id", sourceEventID, "event_type", eventType, "err", handlerErr)
		return nil
	}
	return handlerErr
}

// isPermanent returns true when the error is a wallet domain sentinel
// the aggregate raised against an invariant that won't change on retry.
// Idempotency wins also count — the second delivery hits ErrAlreadyProcessed
// and the record drains.
func isPermanent(err error) bool {
	switch {
	case errors.Is(err, wallet.ErrAlreadyProcessed),
		errors.Is(err, wallet.ErrWalletDisabled),
		errors.Is(err, wallet.ErrCurrencyMismatch),
		errors.Is(err, wallet.ErrInsufficientFunds),
		errors.Is(err, wallet.ErrInvalidAmount),
		errors.Is(err, wallet.ErrInvalidWithdrawCategory),
		errors.Is(err, wallet.ErrInvalidDepositCategory):
		return true
	default:
		return false
	}
}
