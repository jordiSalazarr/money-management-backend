package eventstore_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// KurrentDB runs the identical contract table the memory store runs — both the
// write side and the read side, because unlike DynamoDB it has a global log.
//
// That the same table passes against both is the point of phase 1's port
// split: the memory store was written to imitate this one, so a saga debugged
// in memory behaves the same here.
//
// Skips when KURRENTDB_URL is unset, so unit-only CI stays green. Bring the
// container up with `make kurrentdb-up` and run `make test-kurrent`.
func TestKurrentStoreSatisfiesTheContract(t *testing.T) {
	RunStoreContract(t, "kurrentdb", func(t *testing.T) eventstore.Store {
		return newKurrentTestStore(t)
	})
	RunFeedContract(t, "kurrentdb", func(t *testing.T) (eventstore.Store, eventstore.Feed) {
		s := newKurrentTestStore(t)
		return s, s
	})
}

// newKurrentTestStore returns a store scoped to a fresh stream namespace.
//
// KurrentDB has no "drop database" between tests and the $all log is global
// and append-only, so isolation comes from unique stream names rather than a
// clean server: every test gets its own category, and a feed reading $all is
// filtered to it.
func newKurrentTestStore(t *testing.T) *namespacedStore {
	t.Helper()
	url := os.Getenv("KURRENTDB_URL")
	if url == "" {
		t.Skip("KURRENTDB_URL not set — skipping KurrentDB contract tests")
	}

	store, err := eventstore.NewKurrentStore(url)
	if err != nil {
		t.Fatalf("connecting to kurrentdb at %s: %v", url, err)
	}
	t.Cleanup(func() { _ = store.Close() })

	return &namespacedStore{
		inner:  store,
		prefix: "t" + uuid.NewString()[:8],
	}
}

// namespacedStore prefixes every stream category so concurrent runs of the
// same contract cannot see each other's events on $all.
type namespacedStore struct {
	inner  *eventstore.KurrentStore
	prefix string
}

func (s *namespacedStore) scope(stream eventstore.StreamID) eventstore.StreamID {
	return eventstore.StreamID{Category: s.prefix + stream.Category, ID: stream.ID}
}

func (s *namespacedStore) unscope(stream eventstore.StreamID) eventstore.StreamID {
	return eventstore.StreamID{
		Category: trimPrefix(stream.Category, s.prefix),
		ID:       stream.ID,
	}
}

func (s *namespacedStore) Append(ctx context.Context, stream eventstore.StreamID, events []eventstore.Event, expectedVersion int) error {
	return s.inner.Append(ctx, s.scope(stream), events, expectedVersion)
}

func (s *namespacedStore) Load(ctx context.Context, stream eventstore.StreamID) ([]eventstore.Recorded, error) {
	recorded, err := s.inner.Load(ctx, s.scope(stream))
	for i := range recorded {
		recorded[i].Stream = s.unscope(recorded[i].Stream)
	}
	return recorded, err
}

func (s *namespacedStore) SubscribeAll(ctx context.Context, from eventstore.Position, handler func(eventstore.Recorded) error) error {
	return s.inner.SubscribeAll(ctx, from, func(r eventstore.Recorded) error {
		// $all carries every namespace. Skip anything that is not ours, the
		// same way a real consumer skips categories it does not care about.
		if !strings.HasPrefix(r.Stream.Category, s.prefix) {
			return nil
		}
		r.Stream = s.unscope(r.Stream)
		return handler(r)
	})
}

func trimPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}
