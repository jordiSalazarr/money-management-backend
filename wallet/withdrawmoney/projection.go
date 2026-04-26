package withdrawmoney

// No new projection — MoneyWithdrawn updates the existing Wallets projection:
//   current_balance -= amount
//   last_movement_at = withdrawn_at
//
// Wired in addwallet/projection.go WalletsProjector.Handle()
