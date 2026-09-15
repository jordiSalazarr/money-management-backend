package eventstore

import (
	"context"
	"sync"
)

// MemoryStore is a miniature KurrentDB: streams with optimistic concurrency,
// plus a global ordered log with positions. It satisfies both Store and Feed,
// which is what lets the whole of phase 1 — including the sagas — be built and
// debugged with no infrastructure running.
type MemoryStore struct {
	mu      sync.Mutex
	cond    *sync.Cond
	streams map[string][]int // stream key -> indices into log
	log     []Recorded       // the global ordered log; index is the position
	closed  bool
}

func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{streams: make(map[string][]int)}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *MemoryStore) Append(_ context.Context, stream StreamID, events []Event, expectedVersion int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := stream.String()
	existing := s.streams[key]

	current := NoStream
	if len(existing) > 0 {
		current = s.log[existing[len(existing)-1]].Revision
	}
	if current != expectedVersion {
		return ErrConcurrencyConflict
	}

	for _, e := range events {
		// Positions are 1-based so the zero Position (Start) is unambiguously
		// "before the first event" and can never collide with a real one.
		pos := uint64(len(s.log)) + 1
		rec := Recorded{
			Event:    e,
			Stream:   stream,
			Revision: current + 1,
			Position: Position{Commit: pos, Prepare: pos},
		}
		s.log = append(s.log, rec)
		existing = append(existing, len(s.log)-1)
		current++
	}
	s.streams[key] = existing

	// Wake every live subscriber; each re-checks the log from its own cursor.
	s.cond.Broadcast()
	return nil
}

func (s *MemoryStore) Load(_ context.Context, stream StreamID) ([]Recorded, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.streams[stream.String()]
	if !ok || len(idx) == 0 {
		return nil, ErrAggregateNotFound
	}

	out := make([]Recorded, 0, len(idx))
	for _, i := range idx {
		out = append(out, s.log[i])
	}
	return out, nil
}

func (s *MemoryStore) SubscribeAll(ctx context.Context, from Position, handler func(Recorded) error) error {
	// Positions are 1-based, so a position names an event already seen and the
	// cursor into the 0-based log is exactly its commit. Start is {0,0}, which
	// yields cursor 0 — the whole log — with no special case.
	cursor := int(from.Commit)

	// Unblock the cond wait when ctx is cancelled, so a subscriber parked on an
	// empty log still shuts down.
	stop := context.AfterFunc(ctx, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.cond.Broadcast()
	})
	defer stop()

	for {
		s.mu.Lock()
		for cursor >= len(s.log) && ctx.Err() == nil && !s.closed {
			s.cond.Wait()
		}
		if ctx.Err() != nil {
			s.mu.Unlock()
			return ctx.Err()
		}
		if s.closed && cursor >= len(s.log) {
			s.mu.Unlock()
			return nil
		}
		batch := make([]Recorded, len(s.log)-cursor)
		copy(batch, s.log[cursor:])
		cursor = len(s.log)
		s.mu.Unlock()

		for _, rec := range batch {
			if err := handler(rec); err != nil {
				return err
			}
		}
	}
}

// Close releases subscribers blocked on an empty log. Tests and the in-process
// dev wiring use it; a real store has no equivalent.
func (s *MemoryStore) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	s.cond.Broadcast()
}

var (
	_ Store  = (*MemoryStore)(nil)
	_ Feed   = (*MemoryStore)(nil)
	_ Headed = (*MemoryStore)(nil)
)

// Head returns the position of the last event in the global log, or Start when
// it is empty.
//
// A catch-up subscriber uses it to tell "I have read everything that existed
// when I started" from "I am now waiting for new events", which is also what
// the projector's lag metric is: head minus the stored checkpoint.
func (s *MemoryStore) Head(_ context.Context) (Position, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.log) == 0 {
		return Start, nil
	}
	return s.log[len(s.log)-1].Position, nil
}
