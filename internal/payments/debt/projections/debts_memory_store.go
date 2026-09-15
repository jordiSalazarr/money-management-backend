package projections

import (
	"context"
	"sync"
)

type MemoryDebtStore struct {
	mu    sync.RWMutex
	debts map[string]*DebtView
}

func NewMemoryDebtStore() *MemoryDebtStore {
	return &MemoryDebtStore{
		debts: make(map[string]*DebtView),
	}
}

func (s *MemoryDebtStore) Upsert(_ context.Context, view *DebtView) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := *view
	s.debts[view.ID] = &copy
	return nil
}

func (s *MemoryDebtStore) List(_ context.Context, filter DebtFilter) ([]DebtView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []DebtView
	for _, d := range s.debts {
		if d.OwnerID != filter.OwnerID {
			continue
		}
		if filter.WalletID != "" && d.WalletID != filter.WalletID {
			continue
		}
		if filter.Direction != nil && d.Direction != *filter.Direction {
			continue
		}
		if filter.Status != nil && d.Status != *filter.Status {
			continue
		}
		result = append(result, *d)
	}
	return result, nil
}

func (s *MemoryDebtStore) Get(_ context.Context, id string) (*DebtView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.debts[id]
	if !ok {
		return nil, nil
	}
	out := *d
	return &out, nil
}
