package requesttransfer

// Command opens a transfer. It is a request, not an instruction that must
// succeed: the reactor may still fail it, and TransferFailed will say why.
type Command struct {
	UserID       string
	FromWalletID string
	ToWalletID   string
	Amount       int
	Currency     string
	Reason       string
}
