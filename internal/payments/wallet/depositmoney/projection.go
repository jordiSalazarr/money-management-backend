package depositmoney

// No new projection — MoneyDeposited updates the existing Wallets projection:
//   current_balance += amount
//   last_movement_at = deposited_at
//
// TODO: Wire MoneyDeposited handling into the Wallets projector (addwallet package)
