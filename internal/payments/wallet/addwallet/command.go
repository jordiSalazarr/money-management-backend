package addwallet

type Command struct {
	WalletID      string
	UserID        string
	Name          string
	Description   string
	InitialAmount int
	Currency      string
	Category      string
}
