// Package eventcatalog is the single canonical list of every domain event:
// its current shape, the schema version that shape is at, and a sample used to
// freeze it.
//
// One list, because the alternative is what this codebase had before — seven
// hand-written decode switches, one of which had silently been missing a case
// for long enough that deposit responses were returning an inflated balance.
//
// Adding an event type means adding one Entry here. Changing an event's shape
// means bumping its Version here and adding a fixture; TestEventShapesAreFrozen
// fails until you do. See ADR 0001.
package eventcatalog

import (
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/domain"
	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Entry is one event type in the catalog.
type Entry struct {
	// Version is the schema version the current shape is at. Bump it on every
	// shape change, additive ones included — the version records which shape
	// was written, not which translations exist.
	Version int
	// Factory returns a pointer to the current shape, for unmarshalling.
	Factory func() any
	// Sample is a fully-populated instance of the current shape. It is the
	// input to the frozen-shape tripwire, so every field must be set to a
	// deterministic non-zero value.
	Sample eventstore.DomainEvent
}

// sampleTime is fixed so fixtures are byte-stable across runs.
var sampleTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// Entries is the canonical list. Order is irrelevant to behaviour but kept
// grouped by aggregate for readability.
func Entries() []Entry {
	return []Entry{
		// --- wallet ---
		{Version: 1, Factory: func() any { return &wallet.WalletAdded{} }, Sample: wallet.WalletAdded{
			WalletID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
			Name: "Everyday", Description: "day to day spending", InitialAmount: 150000,
			Currency: "EUR", Category: "Current", CreatedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &wallet.MoneyDeposited{} }, Sample: wallet.MoneyDeposited{
			WalletID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
			Amount: 4200, Currency: "EUR", Reason: "september salary", Category: "Salary",
			DepositedAt: sampleTime, IdempotencyKey: "tx-1:credit",
		}},
		{Version: 1, Factory: func() any { return &wallet.MoneyWithdrawn{} }, Sample: wallet.MoneyWithdrawn{
			WalletID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
			Amount: 1350, Currency: "EUR", Reason: "weekly shop", Category: "Food & Groceries",
			WithdrawnAt: sampleTime, IdempotencyKey: "tx-1:debit",
		}},
		{Version: 1, Factory: func() any { return &wallet.MoneyRefunded{} }, Sample: wallet.MoneyRefunded{
			WalletID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
			Amount: 4200, Currency: "EUR", Reason: "transfer tx-1 could not be credited",
			RefundedAt: sampleTime, IdempotencyKey: "tx-1:refund",
		}},
		{Version: 1, Factory: func() any { return &wallet.WalletDisabled{} }, Sample: wallet.WalletDisabled{
			WalletID: "11111111-1111-1111-1111-111111111111", UserID: "22222222-2222-2222-2222-222222222222",
			DisabledAt: sampleTime, Reason: "no longer used",
		}},

		// --- debt ---
		{Version: 1, Factory: func() any { return &debt.DebtRecorded{} }, Sample: debt.DebtRecorded{
			DebtID: "33333333-3333-3333-3333-333333333333", WalletID: "11111111-1111-1111-1111-111111111111",
			OwnerID: "22222222-2222-2222-2222-222222222222", Amount: 5000, Currency: "EUR",
			Direction: "OUT", Reason: "dinner split", CreatedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &debt.DebtSettlementRequested{} }, Sample: debt.DebtSettlementRequested{
			DebtID:      "33333333-3333-3333-3333-333333333333",
			RequestedBy: "22222222-2222-2222-2222-222222222222", RequestedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &debt.DebtSettlementFailed{} }, Sample: debt.DebtSettlementFailed{
			DebtID: "33333333-3333-3333-3333-333333333333",
			Reason: "insufficient_funds", FailedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &debt.DebtSettlementRetryAttempted{} }, Sample: debt.DebtSettlementRetryAttempted{
			DebtID: "33333333-3333-3333-3333-333333333333", Attempt: 2, AttemptedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &debt.DebtSettled{} }, Sample: debt.DebtSettled{
			DebtID:    "33333333-3333-3333-3333-333333333333",
			SettledBy: "22222222-2222-2222-2222-222222222222", SettledAt: sampleTime,
		}},

		// --- transfer process ---
		{Version: 1, Factory: func() any { return &transfer.TransferRequested{} }, Sample: transfer.TransferRequested{
			TransferID: "55555555-5555-5555-5555-555555555555", UserID: "22222222-2222-2222-2222-222222222222",
			FromWalletID: "11111111-1111-1111-1111-111111111111", ToWalletID: "66666666-6666-6666-6666-666666666666",
			Amount: 5000, Currency: "EUR", Reason: "rent", RequestedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &transfer.TransferDebited{} }, Sample: transfer.TransferDebited{
			TransferID: "55555555-5555-5555-5555-555555555555", DebitedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &transfer.TransferCompleted{} }, Sample: transfer.TransferCompleted{
			TransferID: "55555555-5555-5555-5555-555555555555", CompletedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &transfer.TransferFailed{} }, Sample: transfer.TransferFailed{
			TransferID: "55555555-5555-5555-5555-555555555555", Reason: "insufficient_funds", FailedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &transfer.TransferStuck{} }, Sample: transfer.TransferStuck{
			TransferID: "55555555-5555-5555-5555-555555555555", Reason: "refund_rejected",
			StrandedAmount: 5000, StrandedWalletID: "11111111-1111-1111-1111-111111111111", StuckAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &transfer.TransferRetryAttempted{} }, Sample: transfer.TransferRetryAttempted{
			TransferID: "55555555-5555-5555-5555-555555555555", Attempt: 2, AttemptedAt: sampleTime,
		}},

		// --- recurring movement ---
		{Version: 1, Factory: func() any { return &recurringmovement.RecurringMovementRegistered{} },
			Sample: recurringmovement.RecurringMovementRegistered{
				RecurringMovementID: "44444444-4444-4444-4444-444444444444",
				WalletID:            "11111111-1111-1111-1111-111111111111",
				UserID:              "22222222-2222-2222-2222-222222222222",
				Type:                "withdrawal", Amount: 9900, Currency: "EUR",
				Frequency: "monthly", Reason: "gym membership",
				StartDate: sampleTime, EndDate: sampleTime.AddDate(1, 0, 0), RegisteredAt: sampleTime,
			}},
		// --- occurrence process (one firing of a recurring movement) ---
		{Version: 1, Factory: func() any { return &occurrence.MovementDue{} }, Sample: occurrence.MovementDue{
			OccurrenceID:        "44444444-4444-4444-4444-444444444444-2026-01-02",
			RecurringMovementID: "44444444-4444-4444-4444-444444444444",
			WalletID:            "11111111-1111-1111-1111-111111111111",
			UserID:              "22222222-2222-2222-2222-222222222222",
			Type:                "withdrawal", Amount: 9900, Currency: "EUR", Reason: "gym membership",
			ForDate: sampleTime, DueAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &occurrence.MovementExecuted{} }, Sample: occurrence.MovementExecuted{
			OccurrenceID:        "44444444-4444-4444-4444-444444444444-2026-01-02",
			RecurringMovementID: "44444444-4444-4444-4444-444444444444",
			ForDate:             sampleTime, ExecutedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &occurrence.MovementSkipped{} }, Sample: occurrence.MovementSkipped{
			OccurrenceID:        "44444444-4444-4444-4444-444444444444-2026-01-02",
			RecurringMovementID: "44444444-4444-4444-4444-444444444444",
			ForDate:             sampleTime, Reason: "insufficient_funds", SkippedAt: sampleTime,
		}},
		{Version: 1, Factory: func() any { return &occurrence.MovementRetryAttempted{} }, Sample: occurrence.MovementRetryAttempted{
			OccurrenceID: "44444444-4444-4444-4444-444444444444-2026-01-02",
			Attempt:      2, AttemptedAt: sampleTime,
		}},

		{Version: 1, Factory: func() any { return &recurringmovement.RecurringMovementStopped{} },
			Sample: recurringmovement.RecurringMovementStopped{
				RecurringMovementID: "44444444-4444-4444-4444-444444444444",
				StoppedBy:           "22222222-2222-2222-2222-222222222222",
				Reason:              "cancelled the membership", StoppedAt: sampleTime,
			}},
	}
}

// NewCodec builds the codec every part of the system decodes through.
//
// The upcaster registry ships empty: the store starts empty, so there is
// nothing to upcast yet. The seam exists so the first real schema change is a
// five-line addition rather than a retrofit against live history. When the
// first one lands, its frozen shape goes in the owning context's schema/
// package and is wired in here.
func NewCodec() *eventstore.Codec {
	c := eventstore.NewCodec()
	for _, e := range Entries() {
		c.Register(e.Sample.EventType(), e.Version, e.Factory)
	}
	return c
}
