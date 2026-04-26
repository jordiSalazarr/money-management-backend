package auth

import (
	"sync"
	"time"
)

type User struct {
	ID           string
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

type UserStorer interface {
	Create(user *User) error
	FindByEmail(email string) (*User, error)
}

type MemoryUserStore struct {
	mu    sync.RWMutex
	users map[string]*User // keyed by email
}

func NewMemoryUserStore() *MemoryUserStore {
	return &MemoryUserStore{
		users: make(map[string]*User),
	}
}

func (s *MemoryUserStore) Create(user *User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[user.Email]; exists {
		return ErrEmailTaken
	}
	s.users[user.Email] = user
	return nil
}

func (s *MemoryUserStore) FindByEmail(email string) (*User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	user, exists := s.users[email]
	if !exists {
		return nil, ErrUserNotFound
	}
	return user, nil
}
