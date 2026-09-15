package refundmoney

// Command returns money to a wallet to undo a withdrawal that should not have
// stood.
//
// There is no Category: a refund's category is not a user's choice. There is
// no optional IdempotencyKey either — a refund is only ever issued by a
// process compensating for a failed step, never by a person, so the key is
// always present and always derived from the process (e.g. "<txID>:refund").
type Command struct {
	WalletID       string
	UserID         string
	Amount         int
	Currency       string
	Reason         string
	IdempotencyKey string
}
