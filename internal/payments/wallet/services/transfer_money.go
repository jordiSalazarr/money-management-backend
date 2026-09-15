package walletservices

import (
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
)

type TransferMoneyCommand struct {
	UserID       string
	FromWalletID string
	ToWalletID   string
	Amount       int
	Currency     string
	Reason       string
}

func TransferMoney(fromWallet, toWallet *wallet.Aggregate, cmd TransferMoneyCommand) error {
	now := time.Now()
	if err := fromWallet.Withdraw(cmd.UserID, cmd.Amount, cmd.Currency, cmd.Reason, "Other", "", now); err != nil {
		return err
	}

	if err := toWallet.Deposit(cmd.UserID, cmd.Amount, cmd.Currency, cmd.Reason, "Transfer In", "", now); err != nil {
		return err
	}

	return nil

}
