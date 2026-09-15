// async.go runs the projector, the reactors, the recurring cron and the
// sweeper in-process.
//
// It replaces devdriver.go, which existed because there was no local Kinesis
// stream: that file polled the DynamoDB events table on a ticker, wrapped each
// row in a synthetic Kinesis record, and fed it to the dispatcher. It was a
// fake of a pipeline, and it meant local behaviour diverged from deployed
// behaviour in exactly the places — batch boundaries, ordering, replay — where
// divergence hurts most.
//
// With a global ordered log behind eventstore.Feed, none of that is needed.
// The subscription here is the same one the deployed projector uses; only the
// process boundary differs.
package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	"github.com/jordisalazar/money-management/internal/payments/processes/sweeper"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/projector"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// AsyncOptions controls the in-process pipeline. Zero values pick sensible
// defaults so callers can pass {}.
type AsyncOptions struct {
	// CronInterval is how often the recurring cron runs. Default 30s — fast
	// enough that "a recurring movement fires on its due date" is observable
	// in one dev session, slow enough not to spam the log.
	CronInterval time.Duration

	// SweepInterval is how often the sweeper looks for stalled processes.
	SweepInterval time.Duration

	// Sweeper tunes the threshold and attempt budget.
	Sweeper sweeper.Options
}

func (o AsyncOptions) cronInterval() time.Duration {
	if o.CronInterval <= 0 {
		return 30 * time.Second
	}
	return o.CronInterval
}

func (o AsyncOptions) sweepInterval() time.Duration {
	if o.SweepInterval <= 0 {
		return time.Minute
	}
	return o.SweepInterval
}

// RunAsync starts the read side in the background and returns a cleanup that
// blocks until everything has stopped.
//
// Both consumers subscribe from the beginning of the log on every boot. That
// is safe, and deliberately so: projections derive their row ids from
// meta.EventID and processes carry idempotency keys, so replaying the whole
// log rebuilds the same state instead of duplicating it. Checkpoints (ADR
// 0003) make it *fast*; they are not what makes it *correct*.
func RunAsync(ctx context.Context, log logger.Logger, d deps, opts AsyncOptions) (func(), error) {
	if d.feed == nil {
		return func() {}, errors.New("async pipeline needs a store with a global log; this one has no feed")
	}

	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup

	dispatcher := projector.NewDispatcher(d.codec, slog.Default())
	subscribeProjections(dispatcher, d.queries)

	router := buildReactorRouter(d.store, d.codec)

	// One subscription drives both. Projections run first so that when a
	// reactor appends a new event, the read model already reflects everything
	// that led to it.
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := d.feed.SubscribeAll(ctx, eventstore.Start, func(rec eventstore.Recorded) error {
			if err := dispatcher.Handle(rec); err != nil {
				return err
			}
			return router.Handle(ctx, rec)
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			log.Error("async: subscription stopped", "err", err)
		}
	}()

	cron := &RecurringCronBundle{
		log:      slog.Default(),
		source:   &viewDueItemsSource{views: d.queries.recurring},
		dispatch: NewDueDispatcher(d.store, d.codec),
		Cleanup:  func() {},
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick(ctx, opts.cronInterval(), func() {
			if _, err := cron.Run(ctx); err != nil {
				log.Error("async: recurring cron failed", "err", err)
			}
		})
	}()

	sw := sweeper.New(d.queries.processes, map[string]sweeper.Driver{
		pprojections.KindTransfer:       transfer.NewReactor(d.store, d.codec, nil),
		pprojections.KindOccurrence:     occurrence.NewReactor(d.store, d.codec, nil),
		pprojections.KindDebtSettlement: debtsettlement.NewReactor(d.store, d.codec, nil),
	}, opts.Sweeper, slog.Default())
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick(ctx, opts.sweepInterval(), func() {
			report, err := sw.Sweep(ctx, time.Now().UTC())
			if err != nil {
				log.Error("async: sweep failed", "err", err)
				return
			}
			if report.Examined > 0 {
				log.Info("async: swept stalled processes",
					"examined", report.Examined, "redriven", report.Redriven, "gave_up", report.GaveUp)
			}
		})
	}()

	log.Info("async pipeline running (projector + reactors + cron + sweeper, in process)")

	return func() {
		cancel()
		wg.Wait()
	}, nil
}

func tick(ctx context.Context, every time.Duration, fn func()) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// viewDueItemsSource reads due items off the recurring-movements read model
// through its existing List interface, so it works against the memory store
// and Postgres alike. auroraDueItemsSource pushes the same predicate into SQL
// for the deployed cron, where the table can be large.
type viewDueItemsSource struct {
	views rmprojections.RecurringMovementStore
}

func (s *viewDueItemsSource) DueToday(ctx context.Context) ([]string, error) {
	active := true
	rows, err := s.views.List(ctx, rmprojections.RecurringMovementFilter{Active: &active})
	if err != nil {
		return nil, err
	}

	// Everything whose next due date has passed, not only today's: a cron that
	// missed a run must still catch the days it slept through.
	today := time.Now().UTC().Truncate(24 * time.Hour)
	var due []string
	for _, r := range rows {
		if !r.NextDueDate.After(today) {
			due = append(due, r.ID)
		}
	}
	return due, nil
}
