package transfer

import "errors"

var (
	ErrMissingUserID = errors.New("user_id is required")
	ErrInvalidAmount = errors.New("amount must be greater than 0")
	ErrSameWallet    = errors.New("source and destination wallet must differ")

	// ErrAlreadyDebited is the redelivery signal. It means the step the caller
	// is trying to perform is already recorded, so the right response is to
	// acknowledge and move on — not to retry.
	ErrAlreadyDebited = errors.New("transfer is already debited")

	// ErrAlreadyTerminal means the transfer has reached its one outcome.
	ErrAlreadyTerminal = errors.New("transfer has already reached a terminal state")

	// ErrNotDebited guards the transitions that only make sense once money has
	// actually left the source wallet.
	ErrNotDebited = errors.New("transfer has not been debited")
)
