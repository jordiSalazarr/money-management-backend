package projections

import (
	"context"
	"sort"
	"sync"
	"time"
)

type MemoryProcessStore struct {
	mu        sync.RWMutex
	processes map[string]*ProcessView
}

func NewMemoryProcessStore() *MemoryProcessStore {
	return &MemoryProcessStore{processes: make(map[string]*ProcessView)}
}

func key(kind, id string) string { return kind + "/" + id }

func (s *MemoryProcessStore) Upsert(_ context.Context, view *ProcessView) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stored := *view
	s.processes[key(view.Kind, view.ID)] = &stored
	return nil
}

func (s *MemoryProcessStore) Get(_ context.Context, kind, id string) (*ProcessView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.processes[key(kind, id)]
	if !ok {
		return nil, nil
	}
	out := *v
	return &out, nil
}

func (s *MemoryProcessStore) ListInFlightBefore(_ context.Context, cutoff time.Time) ([]ProcessView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []ProcessView
	for _, v := range s.processes {
		if v.InFlight() && v.RequestedAt.Before(cutoff) {
			out = append(out, *v)
		}
	}
	// Deterministic order so the sweeper behaves the same way twice.
	sort.Slice(out, func(i, j int) bool {
		if out[i].RequestedAt.Equal(out[j].RequestedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].RequestedAt.Before(out[j].RequestedAt)
	})
	return out, nil
}

func (s *MemoryProcessStore) ListByUserID(_ context.Context, userID string) ([]ProcessView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []ProcessView
	for _, v := range s.processes {
		if v.UserID == userID {
			out = append(out, *v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestedAt.After(out[j].RequestedAt) })
	return out, nil
}
