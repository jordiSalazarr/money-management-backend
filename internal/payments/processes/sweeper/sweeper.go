// Package sweeper finds processes that have stopped making progress and gets
// them moving again.
//
// Everything else in the process machinery assumes the reactor runs. This
// exists for the failures where *nothing fails*: the reactor is down for an
// hour, a message is parked before any handler executes, an event is never
// routed. The process sits in a non-terminal state with no outcome event and
// no error anywhere, and before this, nothing noticed — a person just watched
// a spinner forever.
//
// It re-drives rather than times out. Timing out is not safe: a transfer at
// "debited" has already moved money. Re-driving is safe, because every step
// carries a stable idempotency key, so re-running one that already succeeded
// is a no-op. Only after the attempt budget runs out does it settle the
// process, and what it writes then depends on whether money moved.
package sweeper

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/projections"
)

// Driver is what the sweeper needs from a process to look after it. Each
// process implements this over its own stream.
type Driver interface {
	// Redrive continues the process from whatever state it is recorded in.
	Redrive(ctx context.Context, id string) error
	// GiveUp settles the process after the attempt budget is spent.
	GiveUp(ctx context.Context, id string) error
	// RecordAttempt notes a re-drive on the process's own stream, so the count
	// survives a sweeper restart.
	RecordAttempt(ctx context.Context, id string) error
}

// Options tunes when the sweeper intervenes.
type Options struct {
	// Threshold is how long a process may sit un-settled before the sweeper
	// touches it. It must comfortably exceed normal end-to-end latency, or the
	// sweeper will re-drive work that is merely in flight — harmless, thanks
	// to idempotency, but pointless.
	Threshold time.Duration
	// MaxAttempts is how many times to re-drive before giving up.
	MaxAttempts int
}

func (o Options) withDefaults() Options {
	if o.Threshold <= 0 {
		o.Threshold = 10 * time.Minute
	}
	if o.MaxAttempts <= 0 {
		o.MaxAttempts = 3
	}
	return o
}

type Sweeper struct {
	views   projections.ProcessStore
	drivers map[string]Driver
	opts    Options
	log     *slog.Logger
}

func New(views projections.ProcessStore, drivers map[string]Driver, opts Options, log *slog.Logger) *Sweeper {
	if log == nil {
		log = slog.Default()
	}
	return &Sweeper{views: views, drivers: drivers, opts: opts.withDefaults(), log: log}
}

// Report is what one pass did, so a cron invocation can be inspected.
type Report struct {
	Examined  int      `json:"examined"`
	Redriven  int      `json:"redriven"`
	GaveUp    int      `json:"gave_up"`
	Unhandled int      `json:"unhandled"`
	Failures  []string `json:"failures,omitempty"`
}

// Sweep makes one pass. One bad process does not stop the others: each failure
// is collected and the pass continues.
func (s *Sweeper) Sweep(ctx context.Context, now time.Time) (Report, error) {
	cutoff := now.Add(-s.opts.Threshold)

	stalled, err := s.views.ListInFlightBefore(ctx, cutoff)
	if err != nil {
		return Report{}, fmt.Errorf("listing in-flight processes: %w", err)
	}

	var report Report
	for _, view := range stalled {
		report.Examined++

		driver, ok := s.drivers[view.Kind]
		if !ok {
			// A process kind with no driver is a wiring mistake, not a quiet
			// no-op: it means these will stall forever unnoticed.
			report.Unhandled++
			s.log.Error("sweeper: no driver for process kind — these will never recover",
				"kind", view.Kind, "id", view.ID)
			continue
		}

		if view.Attempts >= s.opts.MaxAttempts {
			if err := driver.GiveUp(ctx, view.ID); err != nil {
				report.Failures = append(report.Failures, fmt.Sprintf("%s/%s: giving up: %v", view.Kind, view.ID, err))
				continue
			}
			report.GaveUp++
			s.log.Warn("sweeper: gave up on a stalled process",
				"kind", view.Kind, "id", view.ID, "attempts", view.Attempts, "status", view.Status)
			continue
		}

		if err := driver.RecordAttempt(ctx, view.ID); err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s/%s: recording attempt: %v", view.Kind, view.ID, err))
			continue
		}
		if err := driver.Redrive(ctx, view.ID); err != nil {
			report.Failures = append(report.Failures, fmt.Sprintf("%s/%s: re-driving: %v", view.Kind, view.ID, err))
			continue
		}
		report.Redriven++
		s.log.Info("sweeper: re-drove a stalled process",
			"kind", view.Kind, "id", view.ID, "attempt", view.Attempts+1, "status", view.Status)
	}

	return report, nil
}
