package eventstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/kurrent-io/KurrentDB-Client-Go/kurrentdb"
)

// KurrentStore is a KurrentDB-backed event store. It satisfies both ports:
// Store over individual streams, and Feed over $all.
//
// This is the first adapter where the port's shape is the database's shape
// rather than a translation of it — stream revisions, optimistic concurrency
// on append, and a global ordered log are all native here. The memory store
// was written to imitate it, which is why the same contract table covers both.
type KurrentStore struct {
	client *kurrentdb.Client
	// categories scopes SubscribeAll to the stream categories this application
	// owns. See SubscribeAll for why this is not optional.
	categories []string
}

// NewKurrentStore connects using a kurrentdb:// connection string, e.g.
// "kurrentdb://localhost:2113?tls=false".
//
// categories are the stream categories this application consumes — "wallet",
// "transfer" and so on. They scope the $all subscription; pass none only if
// this application genuinely owns every stream in the database.
func NewKurrentStore(connectionString string, categories ...string) (*KurrentStore, error) {
	config, err := kurrentdb.ParseConnectionString(connectionString)
	if err != nil {
		return nil, fmt.Errorf("parsing kurrentdb connection string: %w", err)
	}
	client, err := kurrentdb.NewClient(config)
	if err != nil {
		return nil, fmt.Errorf("connecting to kurrentdb: %w", err)
	}
	return &KurrentStore{client: client, categories: categories}, nil
}

func (s *KurrentStore) Close() error { return s.client.Close() }

// eventMetadata is what goes in KurrentDB's user metadata, separate from the
// event's data.
//
// The split matters. `data` is the domain event and nothing else, so it stays
// exactly what the aggregate emitted; everything about the envelope lives
// here. schema_version in particular has to be metadata rather than part of
// the type name, or every routing table in the codebase would change on a
// version bump — see ADR 0001.
type eventMetadata struct {
	SchemaVersion int       `json:"schema_version"`
	CorrelationID string    `json:"correlation_id,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	AggregateType string    `json:"aggregate_type"`
}

// Append writes events to a stream at the expected revision.
//
// expectedVersion is the revision of the stream's last event, or NoStream (-1)
// to assert the stream does not exist. That second case is load-bearing: it is
// what makes a deterministically-named stream its own idempotency key, so the
// recurring cron cannot fire the same date twice. See ADR 0002.
func (s *KurrentStore) Append(ctx context.Context, stream StreamID, events []Event, expectedVersion int) error {
	if len(events) == 0 {
		return nil
	}

	data := make([]kurrentdb.EventData, 0, len(events))
	for _, e := range events {
		metadata, err := json.Marshal(eventMetadata{
			SchemaVersion: e.SchemaVersion,
			CorrelationID: e.CorrelationID,
			OccurredAt:    e.OccurredAt.UTC(),
			AggregateType: stream.Category,
		})
		if err != nil {
			return fmt.Errorf("marshalling metadata for %s: %w", e.EventType, err)
		}

		id, err := uuid.Parse(e.EventID)
		if err != nil {
			// The codec generates UUIDs, but a caller building an envelope by
			// hand might not. Rather than fail the append, let the server
			// assign one — the event id is for deduplication, not identity.
			id = uuid.New()
		}

		data = append(data, kurrentdb.EventData{
			EventID:     id,
			EventType:   e.EventType,
			ContentType: kurrentdb.ContentTypeJson,
			Data:        e.Payload,
			Metadata:    metadata,
		})
	}

	_, err := s.client.AppendToStream(ctx, stream.String(), kurrentdb.AppendToStreamOptions{
		StreamState: streamStateFor(expectedVersion),
	}, data...)
	return mapKurrentErr(err)
}

// streamStateFor converts our expectedVersion to KurrentDB's StreamState.
//
// NoStream is -1 in both, which is not a coincidence: the port borrowed the
// convention so the two never need reconciling.
func streamStateFor(expectedVersion int) kurrentdb.StreamState {
	if expectedVersion == NoStream {
		return kurrentdb.NoStream{}
	}
	return kurrentdb.Revision(uint64(expectedVersion))
}

// Load reads a whole stream in order.
func (s *KurrentStore) Load(ctx context.Context, stream StreamID) ([]Recorded, error) {
	read, err := s.client.ReadStream(ctx, stream.String(), kurrentdb.ReadStreamOptions{
		Direction: kurrentdb.Forwards,
		From:      kurrentdb.Start{},
	}, ^uint64(0))
	if err != nil {
		return nil, mapKurrentErr(err)
	}
	defer read.Close()

	var out []Recorded
	for {
		resolved, err := read.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, mapKurrentErr(err)
		}
		if resolved.Event == nil {
			continue
		}

		rec, err := recordedFrom(resolved.Event)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}

	if len(out) == 0 {
		return nil, ErrAggregateNotFound
	}
	return out, nil
}

// SubscribeAll delivers every event after from in the categories this store
// was configured with, then continues live.
//
// The category filter is not an optimisation, it is a correctness boundary.
// $all contains everything in the database: KurrentDB's own system streams,
// anything another application writes, and anything left over from a previous
// life of the database. The codec hard-errors on an event type it does not
// know — deliberately, because for a stream we own that means the registry and
// the log disagree and continuing would silently skip history. But applied to
// $all that same rule is an availability cliff: one foreign event, once, stops
// the projector and every process forever.
//
// Scoping the subscription to our own categories resolves both. Inside them an
// unknown type really is a fault and still halts. Outside them we never look.
//
// This settles the question §7 of the migration plan left open — $all with a
// filter, not a subscription per category, so global ordering is preserved.
func (s *KurrentStore) SubscribeAll(ctx context.Context, from Position, handler func(Recorded) error) error {
	sub, err := s.client.SubscribeToAll(ctx, kurrentdb.SubscribeToAllOptions{
		From:   allPositionFor(from),
		Filter: s.filter(),
	})
	if err != nil {
		return mapKurrentErr(err)
	}
	defer sub.Close()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		event := sub.Recv()
		switch {
		case event.SubscriptionDropped != nil:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("subscription dropped: %w", event.SubscriptionDropped.Error)

		case event.EventAppeared != nil:
			if event.EventAppeared.Event == nil {
				continue
			}
			rec, err := recordedFrom(event.EventAppeared.Event)
			if err != nil {
				return err
			}
			if err := handler(rec); err != nil {
				return err
			}

		default:
			// A checkpoint or caught-up notification. Nothing to hand on.
			continue
		}
	}
}

// filter restricts $all to the configured categories, by stream-name prefix,
// server-side. With none configured it falls back to excluding only system
// events.
func (s *KurrentStore) filter() *kurrentdb.SubscriptionFilter {
	if len(s.categories) == 0 {
		return kurrentdb.ExcludeSystemEventsFilter()
	}

	prefixes := make([]string, 0, len(s.categories))
	for _, c := range s.categories {
		// Stream names render as "<category>-<id>", so the category plus its
		// separator is the prefix. Including the hyphen stops "wallet" from
		// also matching a hypothetical "walletsnapshot" category.
		prefixes = append(prefixes, c+"-")
	}
	return &kurrentdb.SubscriptionFilter{
		Type:     kurrentdb.StreamFilterType,
		Prefixes: prefixes,
	}
}

// allPositionFor resumes after a stored checkpoint.
//
// Start is our zero Position, meaning "before the first event". Every other
// value names an event already handled, and KurrentDB's From is exclusive, so
// it is passed straight through.
func allPositionFor(from Position) kurrentdb.AllPosition {
	if from == Start {
		return kurrentdb.Start{}
	}
	return kurrentdb.Position{Commit: from.Commit, Prepare: from.Prepare}
}

func recordedFrom(e *kurrentdb.RecordedEvent) (Recorded, error) {
	var meta eventMetadata
	if len(e.UserMetadata) > 0 {
		if err := json.Unmarshal(e.UserMetadata, &meta); err != nil {
			return Recorded{}, fmt.Errorf("unmarshalling metadata for %s (%s): %w", e.EventType, e.StreamID, err)
		}
	}

	return Recorded{
		Event: Event{
			EventID:       e.EventID.String(),
			EventType:     e.EventType,
			SchemaVersion: meta.SchemaVersion,
			CorrelationID: meta.CorrelationID,
			Payload:       e.Data,
			OccurredAt:    occurredAt(meta, e),
		},
		Stream:   streamIDFrom(e.StreamID, meta.AggregateType),
		Revision: int(e.EventNumber),
		Position: Position{Commit: e.Position.Commit, Prepare: e.Position.Prepare},
	}, nil
}

// occurredAt prefers the domain's own timestamp over the server's.
//
// They are different facts: CreatedDate is when KurrentDB accepted the append,
// OccurredAt is when the thing happened. Aggregates are handed `now` precisely
// so that stays deterministic, and a replay must not quietly swap in wall-clock
// times from the server.
func occurredAt(meta eventMetadata, e *kurrentdb.RecordedEvent) time.Time {
	if !meta.OccurredAt.IsZero() {
		return meta.OccurredAt
	}
	return e.CreatedDate
}

// streamIDFrom splits "wallet-<uuid>" back into its parts.
//
// It splits on the FIRST hyphen, not the last: an occurrence stream is
// "occurrence-<rmID>-2026-09-15", whose id half contains hyphens of its own.
// The recorded aggregate_type is preferred when present, since that is what
// the writer actually meant.
func streamIDFrom(streamID, category string) StreamID {
	if category != "" {
		return StreamID{Category: category, ID: trimPrefix(streamID, category+"-")}
	}
	for i := 0; i < len(streamID); i++ {
		if streamID[i] == '-' {
			return StreamID{Category: streamID[:i], ID: streamID[i+1:]}
		}
	}
	return StreamID{Category: streamID}
}

func trimPrefix(s, prefix string) string {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// mapKurrentErr collapses KurrentDB's failures into the shared sentinels, so
// no caller has to know which store it is talking to.
func mapKurrentErr(err error) error {
	if err == nil {
		return nil
	}

	var kErr *kurrentdb.Error
	if errors.As(err, &kErr) {
		switch {
		case kErr.IsErrorCode(kurrentdb.ErrorCodeWrongExpectedVersion),
			kErr.IsErrorCode(kurrentdb.ErrorCodeStreamRevisionConflict):
			return ErrConcurrencyConflict
		case kErr.IsErrorCode(kurrentdb.ErrorCodeResourceNotFound):
			return ErrAggregateNotFound
		}
	}
	return fmt.Errorf("kurrentdb: %w", err)
}

var (
	_ Store  = (*KurrentStore)(nil)
	_ Feed   = (*KurrentStore)(nil)
	_ Headed = (*KurrentStore)(nil)
)

// Head returns the position of the last event in $all, or Start when the log
// is empty.
//
// It is what makes a projection's lag measurable: head minus the stored
// checkpoint is how far behind a subscription is, which is the only honest
// thing a projector's liveness probe can report. A catch-up consumer also uses
// it to tell "I have read everything that existed when I started" from "I am
// now waiting".
func (s *KurrentStore) Head(ctx context.Context) (Position, error) {
	read, err := s.client.ReadAll(ctx, kurrentdb.ReadAllOptions{
		Direction: kurrentdb.Backwards,
		From:      kurrentdb.End{},
	}, 1)
	if err != nil {
		return Start, mapKurrentErr(err)
	}
	defer read.Close()

	resolved, err := read.Recv()
	if errors.Is(err, io.EOF) {
		return Start, nil
	}
	if err != nil {
		return Start, mapKurrentErr(err)
	}
	if resolved.Event == nil {
		return Start, nil
	}
	return Position{Commit: resolved.Event.Position.Commit, Prepare: resolved.Event.Position.Prepare}, nil
}
