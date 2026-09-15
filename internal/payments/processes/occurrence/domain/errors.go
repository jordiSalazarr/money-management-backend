package occurrence

import "errors"

var (
	ErrInvalidAmount   = errors.New("amount must be greater than 0")
	ErrInvalidType     = errors.New("type must be deposit or withdrawal")
	ErrAlreadyTerminal = errors.New("occurrence has already reached a terminal state")
)
