package withdrawmoney

type Command struct {
	WalletID string
	UserID   string
	Amount   int
	Currency string
	Reason   string
	Category string
	// IdempotencyKey is left empty for HTTP-initiated withdrawals and set
	// to the source Kinesis event_id by the reactor. The wallet aggregate
	// short-circuits with wallet.ErrAlreadyProcessed on a duplicate key.
	IdempotencyKey string
}
