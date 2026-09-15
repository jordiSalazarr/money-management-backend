package depositmoney

type Command struct {
	WalletID string
	UserID   string
	Amount   int
	Currency string
	Reason   string
	Category string
	// IdempotencyKey is left empty for HTTP-initiated deposits and set to
	// a process-derived key (e.g. "<txID>:credit") by the reactor. The wallet aggregate
	// short-circuits with wallet.ErrAlreadyProcessed on a duplicate key.
	IdempotencyKey string
}
