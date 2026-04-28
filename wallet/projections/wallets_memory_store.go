package projections

import (
	"context"
	"sync"
)

type MemoryWalletStore struct {
	mu      sync.RWMutex
	wallets map[string]*WalletView
}

func NewMemoryWalletStore() *MemoryWalletStore {
	return &MemoryWalletStore{
		wallets: make(map[string]*WalletView),
	}
}

func (s *MemoryWalletStore) Upsert(_ context.Context, view *WalletView) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := *view
	s.wallets[view.ID] = &copy
	return nil
}

func (s *MemoryWalletStore) ListByUserID(_ context.Context, userID string) ([]WalletView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []WalletView
	for _, w := range s.wallets {
		if w.UserID == userID {
			result = append(result, *w)
		}
	}
	return result, nil
}
