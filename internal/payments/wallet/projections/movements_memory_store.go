package projections

import (
	"context"
	"sync"
)

type MemoryMovementStore struct {
	mu        sync.RWMutex
	movements map[string][]MovementView // keyed by wallet_id
}

func NewMemoryMovementStore() *MemoryMovementStore {
	return &MemoryMovementStore{
		movements: make(map[string][]MovementView),
	}
}

func (s *MemoryMovementStore) Insert(_ context.Context, view *MovementView) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.movements[view.WalletID] = append(s.movements[view.WalletID], *view)
	return nil
}

func (s *MemoryMovementStore) ListByWalletID(_ context.Context, walletID string) ([]MovementView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	movements := s.movements[walletID]
	if movements == nil {
		return nil, nil
	}

	result := make([]MovementView, len(movements))
	copy(result, movements)
	return result, nil
}
