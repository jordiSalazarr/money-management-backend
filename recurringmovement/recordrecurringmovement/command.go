package recordrecurringmovement

import "time"

type Command struct {
	WalletID  string
	UserID    string
	Type      string
	Amount    int
	Currency  string
	Frequency string
	Reason    string
	StartDate time.Time
	EndDate   time.Time
}
