package transfermoney

type Command struct {
	UserID       string
	FromWalletID string
	ToWalletID   string
	Amount       int
	Currency     string
	Reason       string
}
