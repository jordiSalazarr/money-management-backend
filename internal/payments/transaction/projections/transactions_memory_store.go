package projections

import (
	"context"
	"sync"
	"time"
)

type MemoryTransactionStore struct {
	mu           sync.RWMutex
	transactions map[string]*TransactionView
}

func NewMemoryTransactionStore() *MemoryTransactionStore {
	return &MemoryTransactionStore{
		transactions: make(map[string]*TransactionView),
	}
}

func (s *MemoryTransactionStore) Insert(_ context.Context, view *TransactionView) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := *view
	s.transactions[view.TransactionID] = &copy
	return nil
}

func (s *MemoryTransactionStore) UpdateStatus(_ context.Context, transactionID string, status string, completedAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	txn, ok := s.transactions[transactionID]
	if !ok {
		return nil
	}
	txn.Status = status
	txn.CompletedAt = completedAt
	return nil
}

func (s *MemoryTransactionStore) ListByUserID(_ context.Context, userID string) ([]TransactionView, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []TransactionView
	for _, txn := range s.transactions {
		if txn.UserID == userID {
			result = append(result, *txn)
		}
	}
	return result, nil
}
