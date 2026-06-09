package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
)

type fakeSource struct {
	ids []string
	err error
}

func (f *fakeSource) DueToday(context.Context) ([]string, error) {
	return f.ids, f.err
}

func newBundleWithFakes(source DueItemsSource, dispatch DispatchExecuteFunc) *RecurringCronBundle {
	return &RecurringCronBundle{
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		source:   source,
		dispatch: dispatch,
		Cleanup:  func() {},
	}
}

func TestRecurringCron_NoDueItems(t *testing.T) {
	b := newBundleWithFakes(&fakeSource{ids: nil}, func(context.Context, string, time.Time) error {
		t.Fatal("dispatch should not be called when there are no due items")
		return nil
	})
	resp, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.DispatchedCount != 0 || resp.SkippedCount != 0 || len(resp.Failures) != 0 {
		t.Errorf("resp = %+v, want zero everything", resp)
	}
}

func TestRecurringCron_AllSuccess(t *testing.T) {
	var calls []string
	b := newBundleWithFakes(
		&fakeSource{ids: []string{"rm-1", "rm-2", "rm-3"}},
		func(_ context.Context, id string, _ time.Time) error {
			calls = append(calls, id)
			return nil
		},
	)
	resp, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp.DispatchedCount != 3 {
		t.Errorf("DispatchedCount = %d, want 3", resp.DispatchedCount)
	}
	if len(calls) != 3 {
		t.Errorf("dispatch called %d times, want 3", len(calls))
	}
}

func TestRecurringCron_AlreadyExecuted_Skipped(t *testing.T) {
	b := newBundleWithFakes(
		&fakeSource{ids: []string{"rm-1"}},
		func(context.Context, string, time.Time) error {
			return recurringmovement.ErrAlreadyExecutedToday
		},
	)
	resp, _ := b.Run(context.Background())
	if resp.DispatchedCount != 0 || resp.SkippedCount != 1 {
		t.Errorf("resp = %+v, want skipped=1", resp)
	}
}

func TestRecurringCron_NotActive_Skipped(t *testing.T) {
	b := newBundleWithFakes(
		&fakeSource{ids: []string{"rm-1"}},
		func(context.Context, string, time.Time) error {
			return recurringmovement.ErrNotActive
		},
	)
	resp, _ := b.Run(context.Background())
	if resp.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", resp.SkippedCount)
	}
}

func TestRecurringCron_PartialFailure_ContinuesAndReports(t *testing.T) {
	custom := errors.New("simulated transient failure")
	b := newBundleWithFakes(
		&fakeSource{ids: []string{"rm-ok", "rm-bad", "rm-ok-2"}},
		func(_ context.Context, id string, _ time.Time) error {
			if id == "rm-bad" {
				return custom
			}
			return nil
		},
	)
	resp, err := b.Run(context.Background())
	if err != nil {
		t.Fatalf("Run should not surface per-row failure as fatal: %v", err)
	}
	if resp.DispatchedCount != 2 {
		t.Errorf("DispatchedCount = %d, want 2", resp.DispatchedCount)
	}
	if len(resp.Failures) != 1 {
		t.Fatalf("Failures = %d, want 1", len(resp.Failures))
	}
}

func TestRecurringCron_SourceError_Fatal(t *testing.T) {
	custom := errors.New("aurora unreachable")
	b := newBundleWithFakes(
		&fakeSource{err: custom},
		func(context.Context, string, time.Time) error { return nil },
	)
	_, err := b.Run(context.Background())
	if err == nil {
		t.Fatal("expected fatal error when source fails")
	}
}
