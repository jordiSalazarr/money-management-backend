package getuserwallets_test

import (
	"time"

	"github.com/jordisalazar/money-management/internal/payments/wallet"
)

func walletAddedEvent(walletID, userID, name, description string, initialAmount int, currency, category string) wallet.WalletAdded {
	return wallet.WalletAdded{
		WalletID:      walletID,
		UserID:        userID,
		Name:          name,
		Description:   description,
		InitialAmount: initialAmount,
		Currency:      currency,
		Category:      category,
		CreatedAt:     time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC),
	}
}
