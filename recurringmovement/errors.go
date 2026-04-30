package recurringmovement

import "errors"

var (
	ErrInvalidAmount    = errors.New("amount must be between 1 and 10000000")
	ErrInvalidCurrency  = errors.New("currency must be EUR or USD")
	ErrInvalidFrequency = errors.New("frequency must be DAILY, WEEKLY, or MONTHLY")
	ErrInvalidType      = errors.New("type must be deposit or withdrawal")
	ErrMissingReason    = errors.New("reason is required")
	ErrInvalidStartDate = errors.New("start_date must be today or in the future")
	ErrInvalidEndDate   = errors.New("end_date must be after start_date")
	ErrMissingWalletID  = errors.New("wallet_id is required")
	ErrMissingUserID        = errors.New("user_id is required")
	ErrNotActive            = errors.New("recurring movement is not active")
	ErrAlreadyExecutedToday = errors.New("recurring movement already executed today")
)
