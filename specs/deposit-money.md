# DepositMoney Slice

## Aggregate

- **Name:** Wallet
- **ID:** `wallet_id` (UUID)
- **Type:** `wallet`

This slice operates on the existing Wallet aggregate. It adds a `Deposit()` method.

## Command: DepositMoney

| Field     | Type   | Required | Notes                            |
|-----------|--------|----------|----------------------------------|
| wallet_id | UUID   | yes      | Target wallet (from URL path)    |
| user_id   | UUID   | yes      | Hardcoded for now (no auth yet)  |
| amount    | Int    | yes      | Cents. Must be > 0               |
| currency  | String | yes      | Must match wallet's currency     |
| reason    | String | no       |                                  |

### Invariants

1. `amount` must be > 0
2. `currency` must match the wallet's existing currency

## Event: MoneyDeposited

| Field        | Type     | Notes                        |
|--------------|----------|------------------------------|
| wallet_id    | UUID     | Aggregate ID                 |
| user_id      | UUID     | Who deposited                |
| amount       | Int      | Cents                        |
| currency     | String   |                              |
| reason       | String   | Empty string if not provided |
| deposited_at | DateTime | Server-generated (UTC)       |

## Projection: Wallets (existing)

`MoneyDeposited` updates the existing Wallets read model:

- `current_balance += amount`
- `last_movement_at = deposited_at`

No new projection — just a new event handler on the existing Wallets projector.

## HTTP Endpoint

- **Method:** `POST`
- **Path:** `/wallets/:id/deposit`
- **Request body:**

```json
{
  "user_id": "uuid",
  "amount": 5000,
  "currency": "EUR",
  "reason": "Birthday gift"
}
```

- **Success:** `200 OK`

```json
{
  "wallet_id": "uuid",
  "new_balance": 55000
}
```

- **Errors:**
  - `400` — `invalid_amount`, `currency_mismatch`, `missing_user_id`
  - `404` — `wallet_not_found`

## Error Codes

| Code              | HTTP | Message                                          |
|-------------------|------|--------------------------------------------------|
| invalid_amount    | 400  | amount must be greater than 0                    |
| currency_mismatch | 400  | currency does not match wallet currency          |
| missing_user_id   | 400  | user_id is required                              |
| wallet_not_found  | 404  | wallet not found                                 |
