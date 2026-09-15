// devdriver.go runs the projector + reactor + recurring-cron pieces
// in-process for local development.
//
// In production those three pieces are separate Lambdas wired to a
// DynamoDB → Kinesis Data Streams export. On a developer laptop there is
// no Kinesis stream, so this file collapses the pipeline into one
// process: a single ticker scans the LocalStack DDB events table, wraps
// each new event in a synthetic Kinesis record matching the wire format
// kinesisdecode expects, and dispatches the batch to the same Dispatcher
// and Router the Lambda entry points use. A second ticker drives the
// recurring-cron bundle.
//
// Cold-boot semantics match developer expectation: pumps start with an
// empty "seen" set, so the dev stack reprocesses every event in the
// table on each restart. Pair with `make reset` to also wipe the DDB
// table + projection tables when you want a clean slate.
//
// This intentionally diverges from prod topology — Kinesis batch
// boundaries, cold starts, and per-shard retry semantics are not
// exercised here. Those belong in a deployed dev environment.

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"

	awsevents "github.com/aws/aws-lambda-go/events"

	"github.com/jordisalazar/money-management/internal/projector/kinesisdecode"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/pkg/logger"
)

// DevAsyncOptions controls the in-process dev pipeline. Zero values pick
// sensible defaults so callers can pass {}.
type DevAsyncOptions struct {
	// PollInterval is how often the event pump scans the DDB events
	// table. Default 100ms — fast enough that UI feels synchronous.
	PollInterval time.Duration

	// CronInterval is how often the recurring-cron handler runs. Default
	// 30s — fast enough that "recurring movement fires on its due date"
	// is observable in a single dev session, slow enough not to spam logs.
	CronInterval time.Duration

	// EventsTable / AuroraURL fall back to env vars when empty.
	EventsTable string
	AuroraURL   string
}

func (o DevAsyncOptions) pollInterval() time.Duration {
	if o.PollInterval <= 0 {
		return 100 * time.Millisecond
	}
	return o.PollInterval
}

func (o DevAsyncOptions) cronInterval() time.Duration {
	if o.CronInterval <= 0 {
		return 30 * time.Second
	}
	return o.CronInterval
}

// RunDevAsync spawns the dev event pump and cron driver, returning a
// cleanup func the caller should defer. The cleanup blocks until all
// goroutines exit and then closes the bundle pools.
//
// Cancel ctx to stop the goroutines.
func RunDevAsync(ctx context.Context, log logger.Logger, opts DevAsyncOptions) (func(), error) {
	eventsTable := opts.EventsTable
	if eventsTable == "" {
		eventsTable = os.Getenv("EVENTS_TABLE")
	}
	if eventsTable == "" {
		return nil, fmt.Errorf("EVENTS_TABLE must be set to run the dev async pipeline")
	}

	proj, err := BuildProjector(ctx, log, ProjectorOptions{AuroraURL: opts.AuroraURL})
	if err != nil {
		return nil, fmt.Errorf("building projector: %w", err)
	}

	react, err := BuildReactor(ctx, log, ReactorOptions{EventsTable: eventsTable})
	if err != nil {
		proj.Cleanup()
		return nil, fmt.Errorf("building reactor: %w", err)
	}

	cron, err := BuildRecurringCron(ctx, log, RecurringCronOptions{
		AuroraURL:   opts.AuroraURL,
		EventsTable: eventsTable,
	})
	if err != nil {
		proj.Cleanup()
		react.Cleanup()
		return nil, fmt.Errorf("building recurring cron: %w", err)
	}

	store, err := eventstore.NewDynamoStore(ctx, eventsTable)
	if err != nil {
		proj.Cleanup()
		react.Cleanup()
		cron.Cleanup()
		return nil, fmt.Errorf("building dev pump store: %w", err)
	}

	log.Info("dev async pipeline starting",
		"events_table", eventsTable,
		"poll_interval", opts.pollInterval(),
		"cron_interval", opts.cronInterval())

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go runEventPump(ctx, wg, log, store, opts.pollInterval(), proj, react)
	go runCronDriver(ctx, wg, log, opts.cronInterval(), cron)

	return func() {
		wg.Wait()
		cron.Cleanup()
		react.Cleanup()
		proj.Cleanup()
	}, nil
}

// runEventPump scans the DDB events table on a ticker, finds events the
// pump hasn't seen yet this session, and fans them out to both the
// projector dispatcher and the reactor router as a single Kinesis batch.
//
// Sharing one `seen` set across both subscribers is fine — every event
// should reach every subscriber exactly once per process lifetime, and a
// single set keeps the two consumers in lockstep on ordering.
func runEventPump(
	ctx context.Context,
	wg *sync.WaitGroup,
	log logger.Logger,
	store *eventstore.DynamoStore,
	interval time.Duration,
	proj *ProjectorBundle,
	react *ReactorBundle,
) {
	defer wg.Done()

	seen := map[string]struct{}{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pumpOnce(ctx, log, store, seen, proj, react)
		}
	}
}

func pumpOnce(
	ctx context.Context,
	log logger.Logger,
	store *eventstore.DynamoStore,
	seen map[string]struct{},
	proj *ProjectorBundle,
	react *ReactorBundle,
) {
	all, err := store.LoadAll(ctx)
	if err != nil {
		log.Warn("dev pump: LoadAll failed", "err", err)
		return
	}

	fresh := make([]eventstore.Event, 0, len(all))
	for _, e := range all {
		if _, ok := seen[e.EventID]; ok {
			continue
		}
		seen[e.EventID] = struct{}{}
		fresh = append(fresh, e)
	}
	if len(fresh) == 0 {
		return
	}

	// DDB Scan returns items in undefined order. Sort by occurred_at then
	// version so projectors and reactors see a deterministic order; ties
	// break by version to keep events for the same aggregate ordered.
	sort.Slice(fresh, func(i, j int) bool {
		if !fresh[i].OccurredAt.Equal(fresh[j].OccurredAt) {
			return fresh[i].OccurredAt.Before(fresh[j].OccurredAt)
		}
		return fresh[i].Version < fresh[j].Version
	})

	records := make([]awsevents.KinesisEventRecord, 0, len(fresh))
	for _, e := range fresh {
		rec, err := wrapAsKinesisRecord(e)
		if err != nil {
			log.Warn("dev pump: wrap failed", "event_id", e.EventID, "err", err)
			continue
		}
		records = append(records, rec)
	}

	if err := proj.Dispatcher.HandleBatch(ctx, records); err != nil {
		log.Warn("dev pump: projector dispatch failed", "err", err)
	}
	if err := react.Router.HandleBatch(ctx, records); err != nil {
		log.Warn("dev pump: reactor dispatch failed", "err", err)
	}
}

// wrapAsKinesisRecord encodes a stored event into the JSON wire format
// kinesisdecode.Unmarshal consumes. Only the attributes the dispatcher
// and router actually read (event_type, event_id, payload) are populated.
func wrapAsKinesisRecord(e eventstore.Event) (awsevents.KinesisEventRecord, error) {
	eventType := e.EventType
	eventID := e.EventID
	payload := string(e.Payload)

	img := kinesisdecode.Image{
		"event_type": kinesisdecode.Attr{S: &eventType},
		"event_id":   kinesisdecode.Attr{S: &eventID},
		"payload":    kinesisdecode.Attr{S: &payload},
	}
	rec := kinesisdecode.Record{EventName: "INSERT"}
	rec.Dynamodb.NewImage = img

	data, err := json.Marshal(rec)
	if err != nil {
		return awsevents.KinesisEventRecord{}, fmt.Errorf("marshalling kinesis record: %w", err)
	}
	return awsevents.KinesisEventRecord{
		EventID: "dev-pump:" + e.EventID,
		Kinesis: awsevents.KinesisRecord{Data: data},
	}, nil
}

// runCronDriver invokes the recurring-cron bundle's Run on a ticker.
// Errors are logged and the loop keeps running — a transient Aurora
// hiccup shouldn't kill the dev stack.
func runCronDriver(
	ctx context.Context,
	wg *sync.WaitGroup,
	log logger.Logger,
	interval time.Duration,
	cron *RecurringCronBundle,
) {
	defer wg.Done()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			resp, err := cron.Run(ctx)
			if err != nil {
				log.Warn("dev cron: run failed", "err", err)
				continue
			}
			if resp.DispatchedCount > 0 || len(resp.Failures) > 0 {
				log.Info("dev cron tick",
					"dispatched", resp.DispatchedCount,
					"skipped", resp.SkippedCount,
					"failures", len(resp.Failures))
			}
		}
	}
}
