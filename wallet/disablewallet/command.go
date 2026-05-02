package disablewallet

import "time"

type Command struct {
	WalletID   string
	UserID     string
	Reason     string
	DisabledAt time.Time
}
