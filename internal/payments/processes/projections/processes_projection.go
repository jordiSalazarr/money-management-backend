// Package projections builds processes_view: one row per process, whatever
// kind it is, carrying where it got to and why.
//
// It is deliberately separate from movements_view, which stays a pure ledger
// where every row changes a balance. The two answer different questions. A
// transfer that failed after the debit already shows up in the ledger as a
// withdrawal followed by a refund — real balance changes that net to zero —
// so nothing needs injecting there. What the ledger cannot show is a process
// that moved no money at all: a skipped standing order, a transfer rejected
// before its debit. That is what this view is for.
//
// One view rather than three also means the sweeper has one table to query.
package projections

import (
	"context"
	"fmt"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/debt/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence/domain"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Process kinds.
const (
	KindTransfer       = "transfer"
	KindOccurrence     = "occurrence"
	KindDebtSettlement = "debt_settlement"
)

// ProcessView is one process and its outcome.
type ProcessView struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	UserID string `json:"user_id"`
	Status string `json:"status"`
	// Reason is the snake_case failure code, empty unless the process failed,
	// was skipped, or is stuck.
	Reason   string `json:"reason,omitempty"`
	Amount   int    `json:"amount"`
	Currency string `json:"currency"`
	// Attempts is how many times the sweeper has re-driven this process.
	Attempts     int        `json:"attempts"`
	FromWalletID string     `json:"from_wallet_id,omitempty"`
	ToWalletID   string     `json:"to_wallet_id,omitempty"`
	RequestedAt  time.Time  `json:"requested_at"`
	SettledAt    *time.Time `json:"settled_at,omitempty"`
}

// terminalStatuses is every status meaning "this process is over", across all
// three kinds.
//
// It has to cover all of them, not just transfers: the sweeper decides what to
// re-drive from this, so a kind whose terminal statuses are missing here would
// be re-driven forever after it had already settled.
var terminalStatuses = map[string]bool{
	transfer.StatusCompleted:  true,
	transfer.StatusFailed:     true,
	transfer.StatusStuck:      true,
	occurrence.StatusExecuted: true,
	occurrence.StatusSkipped:  true,
	statusSettled:             true,
	statusSettlementFailed:    true,
}

// Debt settlement statuses. The debt aggregate's own DebtStatus values describe
// the debt; these describe the settlement process running against it.
const (
	statusSettling         = "settling"
	statusSettled          = "settled"
	statusSettlementFailed = "settlement_failed"
)

// InFlight reports whether this process has not yet reached its one outcome.
// The sweeper looks for exactly these.
func (v ProcessView) InFlight() bool {
	return v.Status != "" && !terminalStatuses[v.Status]
}

type ProcessStore interface {
	Upsert(ctx context.Context, view *ProcessView) error
	Get(ctx context.Context, kind, id string) (*ProcessView, error)
	// ListInFlightBefore returns processes that have not settled and were
	// requested before the cutoff. This is the sweeper's query.
	ListInFlightBefore(ctx context.Context, cutoff time.Time) ([]ProcessView, error)
	ListByUserID(ctx context.Context, userID string) ([]ProcessView, error)
}

type ProcessesProjector struct {
	store ProcessStore
}

func NewProcessesProjector(store ProcessStore) *ProcessesProjector {
	return &ProcessesProjector{store: store}
}

// Handle is idempotent by construction: every row is keyed by (kind, id) taken
// from the domain, never generated, so replaying the log rebuilds the same
// rows rather than duplicating them.
func (p *ProcessesProjector) Handle(event any, _ eventstore.EventMetadata) error {
	ctx := context.Background()

	switch e := event.(type) {
	case transfer.TransferRequested:
		return p.upsert(ctx, &ProcessView{
			Kind:         KindTransfer,
			ID:           e.TransferID,
			UserID:       e.UserID,
			Status:       transfer.StatusRequested,
			Amount:       e.Amount,
			Currency:     e.Currency,
			FromWalletID: e.FromWalletID,
			ToWalletID:   e.ToWalletID,
			RequestedAt:  e.RequestedAt,
		})

	case transfer.TransferDebited:
		return p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusDebited
		})

	case transfer.TransferCompleted:
		settled := e.CompletedAt
		return p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusCompleted
			v.SettledAt = &settled
		})

	case transfer.TransferFailed:
		settled := e.FailedAt
		return p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusFailed
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case transfer.TransferStuck:
		settled := e.StuckAt
		return p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusStuck
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case transfer.TransferRetryAttempted:
		return p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Attempts = e.Attempt
		})

	// --- occurrence ---

	case occurrence.MovementDue:
		return p.upsert(ctx, &ProcessView{
			Kind:         KindOccurrence,
			ID:           e.OccurrenceID,
			UserID:       e.UserID,
			Status:       occurrence.StatusDue,
			Amount:       e.Amount,
			Currency:     e.Currency,
			FromWalletID: e.WalletID,
			RequestedAt:  e.DueAt,
		})

	case occurrence.MovementExecuted:
		settled := e.ExecutedAt
		return p.patch(ctx, KindOccurrence, e.OccurrenceID, func(v *ProcessView) {
			v.Status = occurrence.StatusExecuted
			v.SettledAt = &settled
		})

	case occurrence.MovementSkipped:
		settled := e.SkippedAt
		return p.patch(ctx, KindOccurrence, e.OccurrenceID, func(v *ProcessView) {
			v.Status = occurrence.StatusSkipped
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case occurrence.MovementRetryAttempted:
		return p.patch(ctx, KindOccurrence, e.OccurrenceID, func(v *ProcessView) {
			v.Attempts = e.Attempt
		})

	// --- debt settlement ---
	//
	// Keyed by debt id, so a debt settled again after a failed attempt
	// overwrites its own row. That is right: the row describes the current
	// attempt, and re-opening one clears the previous outcome.

	case debt.DebtSettlementRequested:
		return p.reopen(ctx, KindDebtSettlement, e.DebtID, e.RequestedBy, e.RequestedAt)

	case debt.DebtSettled:
		settled := e.SettledAt
		return p.patch(ctx, KindDebtSettlement, e.DebtID, func(v *ProcessView) {
			v.Status = statusSettled
			v.SettledAt = &settled
		})

	case debt.DebtSettlementFailed:
		settled := e.FailedAt
		return p.patch(ctx, KindDebtSettlement, e.DebtID, func(v *ProcessView) {
			v.Status = statusSettlementFailed
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case debt.DebtSettlementRetryAttempted:
		return p.patch(ctx, KindDebtSettlement, e.DebtID, func(v *ProcessView) {
			v.Attempts = e.Attempt
		})
	}
	return nil
}

// reopen starts a fresh attempt on a process whose row may already exist from
// an earlier, failed one. The previous outcome is cleared, or the row would
// still read as settled and the sweeper would never look at it again.
func (p *ProcessesProjector) reopen(ctx context.Context, kind, id, userID string, at time.Time) error {
	existing, err := p.store.Get(ctx, kind, id)
	if err != nil {
		return fmt.Errorf("reading %s %s: %w", kind, id, err)
	}

	view := &ProcessView{Kind: kind, ID: id, UserID: userID, RequestedAt: at}
	if existing != nil {
		view.Amount = existing.Amount
		view.Currency = existing.Currency
		view.FromWalletID = existing.FromWalletID
	}
	view.Status = statusSettling
	view.Reason = ""
	view.SettledAt = nil
	view.Attempts = 0

	return p.upsert(ctx, view)
}

func (p *ProcessesProjector) upsert(ctx context.Context, view *ProcessView) error {
	if err := p.store.Upsert(ctx, view); err != nil {
		return fmt.Errorf("upserting %s %s: %w", view.Kind, view.ID, err)
	}
	return nil
}

// patch reads the row, applies a change, and writes it back. A missing row is
// logged rather than ignored: it means an event arrived for a process whose
// opening event this projection never saw, which is a real ordering fault.
func (p *ProcessesProjector) patch(ctx context.Context, kind, id string, fn func(*ProcessView)) error {
	view, err := p.store.Get(ctx, kind, id)
	if err != nil {
		return fmt.Errorf("reading %s %s: %w", kind, id, err)
	}
	if view == nil {
		return fmt.Errorf("processes projector: no %s row for %s — its opening event was never projected", kind, id)
	}
	fn(view)
	return p.upsert(ctx, view)
}
