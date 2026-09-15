package wallet

import "errors"

var (
	ErrMissingUserID        = errors.New("user_id is required")
	ErrMissingName          = errors.New("name is required")
	ErrInvalidInitialAmount = errors.New("initial_amount must be between 0 and 10000000")
	ErrInvalidCurrency      = errors.New("currency must be EUR or USD")
	ErrInvalidCategory         = errors.New("category must be a valid wallet category")
	ErrInvalidAmount           = errors.New("amount must be greater than 0")
	ErrCurrencyMismatch        = errors.New("currency does not match wallet currency")
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrInvalidWithdrawCategory = errors.New("category must be a valid expense category")
	ErrInvalidDepositCategory  = errors.New("category must be a valid income category")
	ErrWalletDisabled          = errors.New("wallet is disabled")
	ErrWalletAlreadyDisabled   = errors.New("wallet is already disabled")
	// ErrAlreadyProcessed is returned by Deposit/Withdraw when the supplied
	// IdempotencyKey has already been applied to this aggregate. Reactor
	// retries from Kinesis hit this path; HTTP callers never set the key.
	ErrAlreadyProcessed = errors.New("operation already processed for this idempotency key")
)
