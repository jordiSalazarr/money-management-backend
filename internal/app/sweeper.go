package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jordisalazar/money-management/internal/payments/processes/debtsettlement"
	"github.com/jordisalazar/money-management/internal/payments/processes/occurrence"
	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	"github.com/jordisalazar/money-management/internal/payments/processes/sweeper"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// SweeperBundle is what cmd/sweeper runs: one pass, then exit.
//
// It is a CronJob rather than a long-running loop because that is what it is —
// a periodic check for processes that have stopped making progress. Nothing
// about it needs to stay resident between passes.
type SweeperBundle struct {
	sweeper *sweeper.Sweeper
	Cleanup func()
}

type SweeperOptions struct {
	Threshold   time.Duration
	MaxAttempts int
}

// BuildSweeper wires the read model it queries and a driver per process kind.
//
// Every kind must have a driver. A kind without one is not swept at all, which
// is why sweeper.Sweep reports it loudly rather than skipping quietly — the
// symptom would otherwise be processes that simply never settle.
func BuildSweeper(ctx context.Context, log logger.Logger, opts SweeperOptions) (*SweeperBundle, error) {
	mode, err := resolveStoreMode(Options{DefaultStore: StoreModeKurrentDB})
	if err != nil {
		return nil, fmt.Errorf("resolving STORE: %w", err)
	}
	deps, cleanup, err := buildDeps(ctx, log, mode)
	if err != nil {
		return nil, err
	}
	if !deps.queriesReady {
		cleanup()
		return nil, fmt.Errorf("the sweeper needs the processes read model; set AURORA_URL")
	}

	drivers := map[string]sweeper.Driver{
		pprojections.KindTransfer:       transfer.NewReactor(deps.store, deps.codec, nil),
		pprojections.KindOccurrence:     occurrence.NewReactor(deps.store, deps.codec, nil),
		pprojections.KindDebtSettlement: debtsettlement.NewReactor(deps.store, deps.codec, nil),
	}

	return &SweeperBundle{
		sweeper: sweeper.New(deps.queries.processes, drivers, sweeper.Options{
			Threshold:   opts.Threshold,
			MaxAttempts: opts.MaxAttempts,
		}, slog.Default()),
		Cleanup: cleanup,
	}, nil
}

// Run makes one pass and reports what it did.
func (b *SweeperBundle) Run(ctx context.Context) (sweeper.Report, error) {
	return b.sweeper.Sweep(ctx, time.Now().UTC())
}
