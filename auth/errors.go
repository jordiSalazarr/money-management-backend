package auth

import "errors"

var (
	ErrEmailTaken         = errors.New("email is already registered")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidCredentials = errors.New("invalid email or password")
)
