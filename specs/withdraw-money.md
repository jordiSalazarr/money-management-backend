# WithdrawMoney Slice

## Aggregate

- **Name:** Wallet
- **ID:** `wallet_id` (UUID)
- **Type:** `wallet`

This slice operates on the existing Wallet aggregate. It adds a `Withdraw()` method.

## Command: WithdrawMoney

| Field     | Type   | Required | Notes                                                                                  |
|-----------|--------|----------|----------------------------------------------------------------------------------------|
| wallet_id | UUID   | yes      | Target wallet (from URL path)                                                          |
| user_id   | UUID   | yes      | Hardcoded for now (no auth yet)                                                        |
| amount    | Int    | yes      | Cents. Must be > 0                                                                     |
| currency  | String | yes      | Must match wallet's currency                                                           |
| reason    | String | no       |                                                                                        |
| category  | String | yes      | Enum: `Food`, `Investment`, `Clothing`, `Travel`, `Entertainment`, `Health`, `Housing`, `Transport`, `Education`, `Subscriptions`, `Gifts`, `Other` |

### Invariants

1. `amount` must be > 0
2. `currency` must match the wallet's existing currency
3. `category` must be a valid withdrawal category
4. Wallet must have sufficient balance (`balance >= amount`)

## Event: MoneyWithdrawn

| Field        | Type     | Notes                        |
|--------------|----------|------------------------------|
| wallet_id    | UUID     | Aggregate ID                 |
| user_id      | UUID     | Who withdrew                 |
| amount       | Int      | Cents                        |
| currency     | String   |                              |
| reason       | String   | Empty string if not provided |
| category     | String   |                              |
| withdrawn_at | DateTime | Server-generated (UTC)       |

## Projection: Wallets (existing)

`MoneyWithdrawn` updates the existing Wallets read model:

- `current_balance -= amount`
- `last_movement_at = withdrawn_at`

No new projection — just a new event handler on the existing Wallets projector.

## HTTP Endpoint

- **Method:** `POST`
- **Path:** `/wallets/:id/withdraw`
- **Request body:**

```json
{
  "user_id": "uuid",
  "amount": 3000,
  "currency": "EUR",
  "reason": "Lunch",
  "category": "Food"
}
```

- **Success:** `200 OK`

```json
{
  "wallet_id": "uuid",
  "new_balance": 47000
}
```

- **Errors:**
  - `400` — `invalid_amount`, `currency_mismatch`, `invalid_category`, `missing_user_id`, `insufficient_funds`
  - `404` — `wallet_not_found`

## Error Codes

| Code               | HTTP | Message                                         |
|--------------------|------|-------------------------------------------------|
| invalid_amount     | 400  | amount must be greater than 0                   |
| currency_mismatch  | 400  | currency does not match wallet currency         |
| invalid_category   | 400  | category must be a valid withdrawal category    |
| missing_user_id    | 400  | user_id is required                             |
| insufficient_funds | 400  | insufficient funds                              |
| wallet_not_found   | 404  | wallet not found                                |
