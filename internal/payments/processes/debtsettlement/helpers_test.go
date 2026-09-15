package debtsettlement_test

import (
	"context"
	"testing"

	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
)

func depositTopUp(t *testing.T, f *fixture, walletID string, amount int) {
	t.Helper()
	_, err := depositmoney.NewCommandHandler(f.store, f.codec).Handle(context.Background(), depositmoney.Command{
		WalletID: walletID, UserID: "user-1", Amount: amount,
		Currency: "EUR", Reason: "top up", Category: "Salary",
	})
	if err != nil {
		t.Fatalf("topping up: %v", err)
	}
}
