package transfer_test

import (
	"errors"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/domain"
)

var now = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func requested(t *testing.T) *transfer.Aggregate {
	t.Helper()
	agg, err := transfer.Request("tx-1", "user-1", "wallet-a", "wallet-b", 5000, "EUR", "rent", now)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return agg
}

func TestRequest(t *testing.T) {
	t.Run("starts a transfer in the requested state", func(t *testing.T) {
		agg := requested(t)
		if agg.Status() != transfer.StatusRequested {
			t.Errorf("Status = %q, want %q", agg.Status(), transfer.StatusRequested)
		}
		if agg.Changes()[0].EventType() != "TransferRequested" {
			t.Errorf("EventType = %q, want TransferRequested", agg.Changes()[0].EventType())
		}
	})

	t.Run("rejects a transfer to the same wallet", func(t *testing.T) {
		_, err := transfer.Request("tx-1", "user-1", "wallet-a", "wallet-a", 5000, "EUR", "rent", now)
		if !errors.Is(err, transfer.ErrSameWallet) {
			t.Fatalf("want ErrSameWallet, got %v", err)
		}
	})

	t.Run("rejects a non-positive amount", func(t *testing.T) {
		for _, amount := range []int{0, -1} {
			if _, err := transfer.Request("tx-1", "u", "a", "b", amount, "EUR", "r", now); !errors.Is(err, transfer.ErrInvalidAmount) {
				t.Errorf("amount %d: want ErrInvalidAmount, got %v", amount, err)
			}
		}
	})
}

func TestTheHappyPath(t *testing.T) {
	agg := requested(t)

	if err := agg.MarkDebited(now); err != nil {
		t.Fatalf("mark debited: %v", err)
	}
	if agg.Status() != transfer.StatusDebited {
		t.Fatalf("Status = %q, want debited", agg.Status())
	}

	if err := agg.Complete(now); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if agg.Status() != transfer.StatusCompleted {
		t.Fatalf("Status = %q, want completed", agg.Status())
	}
	if !agg.IsTerminal() {
		t.Error("a completed transfer must be terminal")
	}
}

func TestMoneyMoved(t *testing.T) {
	// MoneyMoved is what the sweeper reads to decide whether giving up means
	// "failed" (safe, nothing moved) or "stuck" (money is out there).
	t.Run("is false before the debit", func(t *testing.T) {
		if requested(t).MoneyMoved() {
			t.Error("nothing has moved yet")
		}
	})
	t.Run("is true once debited", func(t *testing.T) {
		agg := requested(t)
		if err := agg.MarkDebited(now); err != nil {
			t.Fatalf("mark debited: %v", err)
		}
		if !agg.MoneyMoved() {
			t.Error("the source wallet has been debited")
		}
	})
}

func TestIllegalTransitions(t *testing.T) {
	t.Run("cannot complete before debiting", func(t *testing.T) {
		agg := requested(t)
		if err := agg.Complete(now); !errors.Is(err, transfer.ErrNotDebited) {
			t.Fatalf("want ErrNotDebited, got %v", err)
		}
	})

	t.Run("cannot debit twice", func(t *testing.T) {
		agg := requested(t)
		if err := agg.MarkDebited(now); err != nil {
			t.Fatalf("first debit: %v", err)
		}
		// This is the redelivery case. The reactor uses it to tell "already
		// done, ack" from "genuinely conflicting".
		if err := agg.MarkDebited(now); !errors.Is(err, transfer.ErrAlreadyDebited) {
			t.Fatalf("want ErrAlreadyDebited, got %v", err)
		}
	})

	t.Run("cannot change a terminal transfer", func(t *testing.T) {
		terminal := map[string]func() *transfer.Aggregate{
			"completed": func() *transfer.Aggregate {
				a := requested(t)
				_ = a.MarkDebited(now)
				_ = a.Complete(now)
				return a
			},
			"failed": func() *transfer.Aggregate {
				a := requested(t)
				_ = a.Fail("insufficient_funds", now)
				return a
			},
			"stuck": func() *transfer.Aggregate {
				a := requested(t)
				_ = a.MarkDebited(now)
				_ = a.MarkStuck("refund_rejected", now)
				return a
			},
		}
		for name, build := range terminal {
			t.Run(name, func(t *testing.T) {
				agg := build()
				if !agg.IsTerminal() {
					t.Fatalf("%s should be terminal", name)
				}
				if err := agg.Fail("whatever", now); !errors.Is(err, transfer.ErrAlreadyTerminal) {
					t.Errorf("Fail: want ErrAlreadyTerminal, got %v", err)
				}
				if err := agg.Complete(now); err == nil {
					t.Error("Complete on a terminal transfer must fail")
				}
			})
		}
	})
}

func TestFailure(t *testing.T) {
	t.Run("failing before the debit means no money moved", func(t *testing.T) {
		agg := requested(t)
		if err := agg.Fail("currency_mismatch", now); err != nil {
			t.Fatalf("fail: %v", err)
		}
		if agg.Status() != transfer.StatusFailed {
			t.Errorf("Status = %q, want failed", agg.Status())
		}
		if agg.MoneyMoved() {
			t.Error("a transfer that failed before debiting moved no money")
		}
		e := agg.Changes()[len(agg.Changes())-1].(transfer.TransferFailed)
		if e.Reason != "currency_mismatch" {
			t.Errorf("Reason = %q, want currency_mismatch", e.Reason)
		}
	})

	t.Run("stuck requires that money actually moved", func(t *testing.T) {
		// Stuck means "money is in limbo". Reaching it without a debit would
		// be a lie, and would send the sweeper looking for funds to recover
		// that were never taken.
		agg := requested(t)
		if err := agg.MarkStuck("refund_rejected", now); !errors.Is(err, transfer.ErrNotDebited) {
			t.Fatalf("want ErrNotDebited, got %v", err)
		}
	})

	t.Run("stuck records what is stranded and where", func(t *testing.T) {
		agg := requested(t)
		_ = agg.MarkDebited(now)
		if err := agg.MarkStuck("refund_rejected", now); err != nil {
			t.Fatalf("stuck: %v", err)
		}
		e := agg.Changes()[len(agg.Changes())-1].(transfer.TransferStuck)
		if e.StrandedAmount != 5000 || e.StrandedWalletID != "wallet-a" {
			t.Errorf("stuck event must say what is stranded and where: %+v", e)
		}
	})
}

func TestAttemptCounter(t *testing.T) {
	// The counter lives on the stream so it survives a sweeper restart.
	agg := requested(t)
	if agg.Attempts() != 0 {
		t.Fatalf("Attempts = %d, want 0", agg.Attempts())
	}
	for i := 1; i <= 3; i++ {
		if err := agg.RecordAttempt(now); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		if agg.Attempts() != i {
			t.Errorf("Attempts = %d, want %d", agg.Attempts(), i)
		}
	}
	_ = agg.Fail("timed_out", now)
	if err := agg.RecordAttempt(now); !errors.Is(err, transfer.ErrAlreadyTerminal) {
		t.Errorf("a terminal transfer must not accrue attempts, got %v", err)
	}
}

func TestRehydration(t *testing.T) {
	agg := transfer.NewAggregate()
	agg.Apply(transfer.TransferRequested{
		TransferID: "tx-1", UserID: "user-1", FromWalletID: "wallet-a", ToWalletID: "wallet-b",
		Amount: 5000, Currency: "EUR", Reason: "rent", RequestedAt: now,
	})
	agg.Apply(transfer.TransferDebited{TransferID: "tx-1", DebitedAt: now})
	agg.Apply(transfer.TransferRetryAttempted{TransferID: "tx-1", Attempt: 1, AttemptedAt: now})

	if agg.Status() != transfer.StatusDebited {
		t.Errorf("Status = %q, want debited", agg.Status())
	}
	if !agg.MoneyMoved() {
		t.Error("MoneyMoved must survive rehydration")
	}
	if agg.Attempts() != 1 {
		t.Errorf("Attempts = %d, want 1 after rehydration", agg.Attempts())
	}
	if agg.Amount() != 5000 || agg.FromWalletID() != "wallet-a" || agg.ToWalletID() != "wallet-b" {
		t.Error("rehydration lost the transfer's details")
	}
	if len(agg.Changes()) != 0 {
		t.Errorf("Apply must not record changes, got %d", len(agg.Changes()))
	}
}
