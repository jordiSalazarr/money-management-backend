package transaction

import "errors"

var (
	ErrAlreadyCompleted = errors.New("transaction is already completed")
)
