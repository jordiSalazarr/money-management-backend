package withdrawmoney

type Command struct {
	WalletID string
	UserID   string
	Amount   int
	Currency string
	Reason   string
	Category string
}
