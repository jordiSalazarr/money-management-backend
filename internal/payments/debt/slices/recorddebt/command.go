package recorddebt

type Command struct {
	DebtID    string
	WalletID  string
	OwnerID   string
	Amount    int
	Currency  string
	Direction string
	Reason    string
}
