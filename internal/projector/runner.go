package projector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Projection is one read model, and everything needed to run it independently.
//
// Build takes a DBTX rather than the projection being constructed once, because
// each event is applied inside its own transaction and the store has to be
// bound to that transaction. Handing it a pool at construction and a
// transaction at write time is what lets the rows and the checkpoint commit
// together.
type Projection struct {
	// Name is the subscription name and the checkpoint key. Changing it starts
	// the projection over from the beginning of the log.
	Name  string
	Build func(db database.DBTX) eventstore.Subscriber
}

// Transactor runs fn inside a transaction.
//
// Memory mode has no database, so its implementation just calls fn(nil) — the
// in-memory stores ignore the DBTX entirely. The interface exists so the
// runner does not have to know which it is talking to.
type Transactor interface {
	InTx(ctx context.Context, fn func(db database.DBTX) error) error
}

// Runner drives one projection: its own subscription, its own checkpoint.
//
// One per projection rather than one for all of them (ADR 0003). That is what
// makes rebuilding a single read model "truncate its table, delete its
// checkpoint row, restart" — it catches up alone while the others keep
// serving. Adding an upcaster makes that a routine operation rather than a
// full replay of everything.
type Runner struct {
	projection  Projection
	feed        eventstore.Feed
	codec       *eventstore.Codec
	checkpoints CheckpointStore
	tx          Transactor
	log         *slog.Logger
}

func NewRunner(
	projection Projection,
	feed eventstore.Feed,
	codec *eventstore.Codec,
	checkpoints CheckpointStore,
	tx Transactor,
	log *slog.Logger,
) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{
		projection:  projection,
		feed:        feed,
		codec:       codec,
		checkpoints: checkpoints,
		tx:          tx,
		log:         log,
	}
}

func (r *Runner) Name() string { return r.projection.Name }

// Run resumes from this projection's checkpoint and keeps going until ctx is
// cancelled or an event cannot be applied.
//
// Stopping on a failed event is deliberate. The position has not advanced, so
// a restart retries exactly that event — whereas skipping it would leave a
// hole that nothing would ever notice or repair.
func (r *Runner) Run(ctx context.Context) error {
	from, err := r.checkpoints.Load(ctx, r.projection.Name)
	if err != nil {
		return err
	}
	if from == eventstore.Start {
		r.log.Info("projection starting from the beginning of the log", "projection", r.projection.Name)
	} else {
		r.log.Info("projection resuming", "projection", r.projection.Name, "from_commit", from.Commit)
	}

	err = r.feed.SubscribeAll(ctx, from, func(rec eventstore.Recorded) error {
		return r.apply(ctx, rec)
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("projection %s: %w", r.projection.Name, err)
	}
	return nil
}

// apply handles one event and advances the checkpoint, both inside one
// transaction.
func (r *Runner) apply(ctx context.Context, rec eventstore.Recorded) error {
	decoded, err := r.codec.Decode(rec)
	if err != nil {
		return fmt.Errorf("projection %s: decoding %s at %s/%d: %w",
			r.projection.Name, rec.EventType, rec.Stream, rec.Revision, err)
	}

	return r.tx.InTx(ctx, func(db database.DBTX) error {
		if err := r.projection.Build(db).Handle(decoded, rec.Meta()); err != nil {
			return fmt.Errorf("projection %s: applying %s at %s/%d: %w",
				r.projection.Name, rec.EventType, rec.Stream, rec.Revision, err)
		}
		return r.checkpoints.Save(ctx, db, r.projection.Name, rec.Position)
	})
}

// NoTransactor runs fn directly, with no database and no transaction. Memory
// mode uses it: there is nothing to roll back, and the in-memory stores ignore
// the DBTX they are handed.
type NoTransactor struct{}

func (NoTransactor) InTx(ctx context.Context, fn func(db database.DBTX) error) error {
	return fn(nil)
}

var _ Transactor = NoTransactor{}
