// Package streams names every stream in the payments context.
//
// It lives outside the domain packages on purpose: a StreamID is an event-store
// concept, and the DDD rule in CLAUDE.md is that domain code imports no
// infrastructure. Keeping the names in one place also makes the stream
// categories — which are what KurrentDB's $ce- projections key off — readable
// as a set rather than scattered across nine command handlers.
package streams

import (
	"time"

	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Categories. These become "$ce-wallet", "$ce-transfer" and so on.
const (
	CategoryWallet     = "wallet"
	CategoryDebt       = "debt"
	CategoryRecurring  = "recurringmovement"
	CategoryTransfer   = "transfer"
	CategoryOccurrence = "occurrence"
)

// Wallet names a wallet's stream.
func Wallet(walletID string) eventstore.StreamID {
	return eventstore.StreamID{Category: CategoryWallet, ID: walletID}
}

// Debt names a debt's stream. A debt settlement is one-shot, so the process
// lives on the debt's own stream rather than getting one of its own.
func Debt(debtID string) eventstore.StreamID {
	return eventstore.StreamID{Category: CategoryDebt, ID: debtID}
}

// RecurringMovement names the stream holding a standing instruction. It holds
// only the instruction — registered, stopped — and stays bounded forever,
// because each firing gets its own stream. See Occurrence.
func RecurringMovement(recurringMovementID string) eventstore.StreamID {
	return eventstore.StreamID{Category: CategoryRecurring, ID: recurringMovementID}
}

// Transfer names a transfer process's stream.
func Transfer(transferID string) eventstore.StreamID {
	return eventstore.StreamID{Category: CategoryTransfer, ID: transferID}
}

// OccurrenceDateLayout is the date form used in an occurrence stream name.
const OccurrenceDateLayout = "2006-01-02"

// Occurrence names the stream for one firing of a recurring movement, on one
// date: "occurrence-<rmID>-2026-09-15".
//
// The name is derived rather than generated, which is the whole point:
// appending MovementDue at ExpectedRevision.NoStream fails with
// WrongExpectedVersion if that date already fired, so the stream identity IS
// the idempotency key. No dedup table, no key to remember to pass.
// See ADR 0002.
func Occurrence(recurringMovementID string, forDate time.Time) eventstore.StreamID {
	return eventstore.StreamID{
		Category: CategoryOccurrence,
		ID:       recurringMovementID + "-" + forDate.UTC().Format(OccurrenceDateLayout),
	}
}

// OccurrenceByID rebuilds an occurrence stream from the id the read model
// carries, which is already "<rmID>-<date>".
func OccurrenceByID(occurrenceID string) eventstore.StreamID {
	return eventstore.StreamID{Category: CategoryOccurrence, ID: occurrenceID}
}
