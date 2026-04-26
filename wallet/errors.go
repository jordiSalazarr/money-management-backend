package wallet

import "errors"

var (
	ErrMissingUserID        = errors.New("user_id is required")
	ErrMissingName          = errors.New("name is required")
	ErrInvalidInitialAmount = errors.New("initial_amount must be between 0 and 10000000")
	ErrInvalidCurrency      = errors.New("currency must be EUR or USD")
	ErrInvalidCategory      = errors.New("category must be Saving, Investment, or Cash")
	ErrInvalidAmount            = errors.New("amount must be greater than 0")
	ErrCurrencyMismatch         = errors.New("currency does not match wallet currency")
	ErrInsufficientFunds        = errors.New("insufficient funds")
	ErrInvalidWithdrawCategory  = errors.New("category must be a valid withdrawal category")
)
