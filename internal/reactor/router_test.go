package reactor_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/reactor"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// buildKinesisRecord crafts the DDB-stream JSON shape the router decodes.
// payload may be nil to omit the attribute; eventType empty leaves the
// attribute missing.
func buildKinesisRecord(t *testing.T, eventName, eventType, eventID string, payload any) events.KinesisEventRecord {
	t.Helper()
	image := map[string]map[string]string{}
	if eventID != "" {
		image["event_id"] = map[string]string{"S": eventID}
	}
	if eventType != "" {
		image["event_type"] = map[string]string{"S": eventType}
	}
	if payload != nil {
		raw, _ := json.Marshal(payload)
		image["payload"] = map[string]string{"S": string(raw)}
	}
	body := map[string]any{
		"eventName": eventName,
		"dynamodb":  map[string]any{"NewImage": image},
	}
	data, _ := json.Marshal(body)
	return events.KinesisEventRecord{
		EventID: eventID,
		Kinesis: events.KinesisRecord{Data: data},
	}
}

func silentLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRouter_RoutesDebtSettled(t *testing.T) {
	var got debt.DebtSettled
	var gotMeta eventstore.EventMetadata
	debtFn := func(ctx context.Context, e debt.DebtSettled, meta eventstore.EventMetadata) error {
		got = e
		gotMeta = meta
		return nil
	}
	recFn := func(ctx context.Context, e recurringmovement.RecurringMovementExecuted, meta eventstore.EventMetadata) error {
		t.Fatal("recurring handler should not have been called")
		return nil
	}
	r := reactor.NewRouter(silentLog(), debtFn, recFn)

	rec := buildKinesisRecord(t, "INSERT", debt.EventTypeDebtSettled, "evt-1",
		debt.DebtSettled{DebtID: "debt-1", SettledBy: "user-1", SettledAt: time.Now().UTC()})
	if err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec}); err != nil {
		t.Fatalf("HandleBatch: %v", err)
	}
	if got.DebtID != "debt-1" {
		t.Errorf("debt handler saw DebtID=%q, want debt-1", got.DebtID)
	}
	if gotMeta.EventID != "evt-1" {
		t.Errorf("meta.EventID=%q, want evt-1", gotMeta.EventID)
	}
}

func TestRouter_RoutesRecurringExecuted(t *testing.T) {
	called := false
	debtFn := func(ctx context.Context, e debt.DebtSettled, meta eventstore.EventMetadata) error {
		t.Fatal("debt handler should not have been called")
		return nil
	}
	recFn := func(ctx context.Context, e recurringmovement.RecurringMovementExecuted, meta eventstore.EventMetadata) error {
		called = true
		if e.RecurringMovementID != "rm-1" {
			t.Errorf("RecurringMovementID=%q", e.RecurringMovementID)
		}
		return nil
	}
	r := reactor.NewRouter(silentLog(), debtFn, recFn)

	rec := buildKinesisRecord(t, "INSERT", recurringmovement.EventTypeRecurringMovementExecuted, "evt-rm-1",
		recurringmovement.RecurringMovementExecuted{
			RecurringMovementID: "rm-1",
			ExecutedForDate:     time.Now().UTC(),
			ExecutedAt:          time.Now().UTC(),
		})
	if err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec}); err != nil {
		t.Fatalf("HandleBatch: %v", err)
	}
	if !called {
		t.Fatal("recurring handler should have been called")
	}
}

func TestRouter_UnroutedEventType_Skipped(t *testing.T) {
	r := reactor.NewRouter(silentLog(),
		func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error {
			t.Fatal("debt handler should not have been called")
			return nil
		},
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			t.Fatal("recurring handler should not have been called")
			return nil
		})

	rec := buildKinesisRecord(t, "INSERT", "WalletAdded", "evt-w",
		map[string]string{"wallet_id": "w-1"})
	if err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec}); err != nil {
		t.Fatalf("HandleBatch should swallow unrouted types: %v", err)
	}
}

func TestRouter_REMOVE_Skipped(t *testing.T) {
	r := reactor.NewRouter(silentLog(),
		func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error {
			t.Fatal("should not be called")
			return nil
		},
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			return nil
		})

	rec := buildKinesisRecord(t, "REMOVE", debt.EventTypeDebtSettled, "evt-r",
		debt.DebtSettled{DebtID: "debt-x"})
	if err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec}); err != nil {
		t.Fatalf("REMOVE should be skipped, got %v", err)
	}
}

func TestRouter_PermanentError_DropsRecord(t *testing.T) {
	debtFn := func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error {
		return wallet.ErrInsufficientFunds
	}
	r := reactor.NewRouter(silentLog(), debtFn,
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			return nil
		})

	rec := buildKinesisRecord(t, "INSERT", debt.EventTypeDebtSettled, "evt-perm",
		debt.DebtSettled{DebtID: "debt-x"})
	err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec})
	if err != nil {
		t.Fatalf("permanent error must drain, got %v", err)
	}
}

func TestRouter_AlreadyProcessed_Drained(t *testing.T) {
	debtFn := func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error {
		return wallet.ErrAlreadyProcessed
	}
	r := reactor.NewRouter(silentLog(), debtFn,
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			return nil
		})

	rec := buildKinesisRecord(t, "INSERT", debt.EventTypeDebtSettled, "evt-dup",
		debt.DebtSettled{DebtID: "debt-x"})
	if err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec}); err != nil {
		t.Errorf("idempotency retry must drain, got %v", err)
	}
}

func TestRouter_RetryableError_Bubbles(t *testing.T) {
	retryable := fmt.Errorf("transient: %w", eventstore.ErrConcurrencyConflict)
	debtFn := func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error {
		return retryable
	}
	r := reactor.NewRouter(silentLog(), debtFn,
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			return nil
		})

	rec := buildKinesisRecord(t, "INSERT", debt.EventTypeDebtSettled, "evt-busy",
		debt.DebtSettled{DebtID: "debt-x"})
	err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec})
	if err == nil {
		t.Fatal("retryable error must bubble up so kinesis retries the batch")
	}
	if !errors.Is(err, eventstore.ErrConcurrencyConflict) {
		t.Errorf("err = %v, want wrapped ErrConcurrencyConflict", err)
	}
}

func TestRouter_MalformedRecord_ReturnsError(t *testing.T) {
	r := reactor.NewRouter(silentLog(),
		func(context.Context, debt.DebtSettled, eventstore.EventMetadata) error { return nil },
		func(context.Context, recurringmovement.RecurringMovementExecuted, eventstore.EventMetadata) error {
			return nil
		})

	rec := events.KinesisEventRecord{EventID: "evt-bad", Kinesis: events.KinesisRecord{Data: []byte("not json")}}
	err := r.HandleBatch(context.Background(), []events.KinesisEventRecord{rec})
	if err == nil {
		t.Fatal("expected error for malformed record")
	}
}
