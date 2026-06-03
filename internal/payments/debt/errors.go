package debt

import "errors"

var (
	ErrInvalidAmount    = errors.New("amount must be between 1 and 10000000")
	ErrInvalidCurrency  = errors.New("currency must be EUR or USD")
	ErrInvalidDirection = errors.New("direction must be IN or OUT")
	ErrMissingReason    = errors.New("reason is required")
	ErrMissingOwnerID   = errors.New("owner_id is required")
	ErrMissingWalletID  = errors.New("wallet_id is required")
	ErrDebtAlreadySettled = errors.New("debt is already settled")
	ErrMissingSettledBy   = errors.New("settled_by is required")
)
