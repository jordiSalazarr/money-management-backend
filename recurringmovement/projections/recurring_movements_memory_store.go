package projections

import (
	"context"
	"sync"
)

type MemoryRecurringMovementStore struct {
	mu    sync.RWMutex
	items map[string]*RecurringMovementView
}

func NewMemoryRecurringMovementStore() *MemoryRecurringMovementStore {
	return &MemoryRecurringMovementStore{
		items: make(map[string]*RecurringMovementView),
	}
}

func (s *MemoryRecurringMovementStore) Upsert(_ context.Context, view *RecurringMovementView) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := *view
	s.items[view.ID] = &copy
	return nil
}

func (s *MemoryRecurringMovementStore) List(_ context.Context, filter RecurringMovementFilter) ([]RecurringMovementView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []RecurringMovementView
	for _, item := range s.items {
		if filter.WalletID != "" && item.WalletID != filter.WalletID {
			continue
		}
		if filter.Active != nil && item.Active != *filter.Active {
			continue
		}
		result = append(result, *item)
	}
	return result, nil
}
