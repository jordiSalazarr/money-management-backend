# DisableWallet Slice

## Aggregate

- **Name:** Wallet
- **ID:** `wallet_id` (UUID)
- **Type:** `wallet`

This slice operates on the existing Wallet aggregate. It adds a `Disable()` method and a `disabled` state field. Disabling a wallet is a soft-delete — the wallet remains visible but blocks all operations.

### New State

| Field    | Type | Default |
|----------|------|---------|
| disabled | bool | false   |

### Guard on Existing Commands

`Deposit()`, `Withdraw()`, and transfer commands must check `disabled` **before any other validation** and return `ErrWalletDisabled`.

## Command: DisableWallet

| Field     | Type     | Required | Notes                          |
|-----------|----------|----------|--------------------------------|
| wallet_id | UUID     | yes      | Target wallet (from URL path)  |
| user_id   | UUID     | yes      | From JWT context               |
| reason    | String   | no       | Why the wallet is being disabled |
| disabled_at | DateTime | yes    | Server-generated (UTC)         |

### Invariants

1. Wallet must not already be disabled (`ErrWalletAlreadyDisabled`)

## Event: WalletDisabled

| Field       | Type     | Notes                        |
|-------------|----------|------------------------------|
| wallet_id   | UUID     | Aggregate ID                 |
| user_id     | UUID     | Who disabled it              |
| disabled_at | DateTime | Server-generated (UTC)       |
| reason      | String   | Empty string if not provided |

## Projection: Wallets (existing)

`WalletDisabled` updates the existing Wallets read model:

- `disabled = true`

Disabled wallets are always sorted last in query results. Active wallets appear first, preserving existing sort order within each group.

No new projection — just a new event handler on the existing Wallets projector.

## Transfer Guard

Both source and destination wallets must not be disabled before initiating a transfer. If either wallet has `disabled == true`, return `ErrWalletDisabled`.

## HTTP Endpoint

- **Method:** `POST`
- **Path:** `/wallets/:id/disable`
- **Request body:**

```json
{
  "reason": "No longer needed"
}
```

- **Success:** `200 OK`

```json
{
  "wallet_id": "uuid"
}
```

- **Errors:**
  - `404` — `wallet_not_found`
  - `409` — `wallet_already_disabled`

## Error Codes

| Code                     | HTTP | Message                        |
|--------------------------|------|--------------------------------|
| wallet_already_disabled  | 409  | wallet is already disabled     |
| wallet_not_found         | 404  | wallet not found               |

### Cross-Slice Error (added to aggregate errors)

| Code             | HTTP | Message            | Used by                                    |
|------------------|------|--------------------|--------------------------------------------|
| wallet_disabled  | 409  | wallet is disabled | depositmoney, withdrawmoney, transaction   |

## Aggregate Test Cases

| Given                            | When                              | Then                                         |
|----------------------------------|-----------------------------------|----------------------------------------------|
| `WalletAdded`                    | `Disable(userID, "no longer needed", now)` | `WalletDisabled` emitted, `disabled == true` |
| `WalletAdded`, `WalletDisabled`  | `Disable(...)`                    | `ErrWalletAlreadyDisabled`                   |
| `WalletAdded`, `WalletDisabled`  | `Deposit(...)`                    | `ErrWalletDisabled`                          |
| `WalletAdded`, `WalletDisabled`  | `Withdraw(...)`                   | `ErrWalletDisabled`                          |
| `WalletAdded`, `WalletDisabled`  | `Transfer(to this wallet)`        | `ErrWalletDisabled`                          |
| `WalletAdded`, `WalletDisabled`  | `Transfer(from this wallet)`      | `ErrWalletDisabled`                          |
