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
	"log/slog"
	"time"

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

// InFlight reports whether this process has not yet reached its one outcome.
// The sweeper looks for exactly these.
func (v ProcessView) InFlight() bool {
	switch v.Status {
	case transfer.StatusCompleted, transfer.StatusFailed, transfer.StatusStuck:
		return false
	}
	return v.Status != ""
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
func (p *ProcessesProjector) Handle(event any, _ eventstore.EventMetadata) {
	ctx := context.Background()

	switch e := event.(type) {
	case transfer.TransferRequested:
		p.upsert(ctx, &ProcessView{
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
		p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusDebited
		})

	case transfer.TransferCompleted:
		settled := e.CompletedAt
		p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusCompleted
			v.SettledAt = &settled
		})

	case transfer.TransferFailed:
		settled := e.FailedAt
		p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusFailed
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case transfer.TransferStuck:
		settled := e.StuckAt
		p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Status = transfer.StatusStuck
			v.Reason = e.Reason
			v.SettledAt = &settled
		})

	case transfer.TransferRetryAttempted:
		p.patch(ctx, KindTransfer, e.TransferID, func(v *ProcessView) {
			v.Attempts = e.Attempt
		})
	}
}

func (p *ProcessesProjector) upsert(ctx context.Context, view *ProcessView) {
	if err := p.store.Upsert(ctx, view); err != nil {
		slog.Error("processes projector: upsert failed",
			"kind", view.Kind, "id", view.ID, "error", err)
	}
}

// patch reads the row, applies a change, and writes it back. A missing row is
// logged rather than ignored: it means an event arrived for a process whose
// opening event this projection never saw, which is a real ordering fault.
func (p *ProcessesProjector) patch(ctx context.Context, kind, id string, fn func(*ProcessView)) {
	view, err := p.store.Get(ctx, kind, id)
	if err != nil {
		slog.Error("processes projector: read failed", "kind", kind, "id", id, "error", err)
		return
	}
	if view == nil {
		slog.Error("processes projector: no row to update — the opening event was never projected",
			"kind", kind, "id", id)
		return
	}
	fn(view)
	p.upsert(ctx, view)
}
