# AddWallet Slice

## Aggregate

- **Name:** Wallet
- **ID:** `wallet_id` (UUID, server-generated)
- **Type:** `wallet`

## Command: AddWallet

| Field          | Type   | Required | Notes                                  |
|----------------|--------|----------|----------------------------------------|
| wallet_id      | UUID   | yes      | Server-generated                       |
| user_id        | UUID   | yes      | Hardcoded for now (no auth yet)        |
| name           | String | yes      |                                        |
| description    | String | no       |                                        |
| initial_amount | Int    | yes      | Cents. Range: 0–10_000_000             |
| currency       | String | yes      | Enum: `EUR`, `USD`                     |
| category       | String | yes      | Enum: `Saving`, `Investment`, `Cash`   |

### Invariants

1. `initial_amount` must be >= 0 and <= 10_000_000
2. `currency` must be `EUR` or `USD`
3. `category` must be one of `Saving`, `Investment`, `Cash`
4. User can have a maximum of 10 wallets — **checked before dispatching** (query wallet count by `user_id` from projection, not an aggregate invariant)

## Event: WalletAdded

| Field          | Type     | Notes                        |
|----------------|----------|------------------------------|
| wallet_id      | UUID     | Aggregate ID                 |
| user_id        | UUID     |                              |
| name           | String   |                              |
| description    | String   | Empty string if not provided |
| initial_amount | Int      | Cents                        |
| currency       | String   |                              |
| category       | String   |                              |
| created_at     | DateTime | Server-generated (UTC)       |

## Projection: Wallets

Read model built from `WalletAdded` events (and future deposit/withdraw events).

| Field            | Type     | Notes                                    |
|------------------|----------|------------------------------------------|
| id               | UUID     | = wallet_id                              |
| user_id          | UUID     |                                          |
| name             | String   |                                          |
| description      | String   |                                          |
| current_balance  | Int      | Starts as `initial_amount`               |
| currency         | String   |                                          |
| category         | String   |                                          |
| created_at       | DateTime |                                          |
| last_movement_at | DateTime | Nullable — null on creation              |

## HTTP Endpoint

- **Method:** `POST`
- **Path:** `/wallets`
- **Request body:**

```json
{
  "user_id": "uuid",
  "name": "My savings",
  "description": "optional",
  "initial_amount": 50000,
  "currency": "EUR",
  "category": "Saving"
}
```

- **Success:** `201 Created`

```json
{
  "wallet_id": "generated-uuid"
}
```

- **Errors:**
  - `400` — `invalid_initial_amount`, `invalid_currency`, `invalid_category`, `missing_name`, `missing_user_id`
  - `409` — `max_wallets_reached` (user already has 10 wallets)

## Error Codes

| Code                   | HTTP | Message                                      |
|------------------------|------|----------------------------------------------|
| invalid_initial_amount | 400  | initial_amount must be between 0 and 10000000 |
| invalid_currency       | 400  | currency must be EUR or USD                  |
| invalid_category       | 400  | category must be Saving, Investment, or Cash |
| missing_name           | 400  | name is required                             |
| missing_user_id        | 400  | user_id is required                          |
| max_wallets_reached    | 409  | user cannot have more than 10 wallets        |
