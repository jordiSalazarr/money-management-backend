# GetWalletMovements Slice

## Aggregate

No aggregate — this is a read-only slice.

## Query: GetWalletMovements

| Field     | Type | Required | Notes          |
|-----------|------|----------|----------------|
| wallet_id | UUID | yes      | From URL path  |

## Projection: Movements (new)

New read model built from `MoneyDeposited`, `MoneyWithdrawn`, and `TransactionCompleted` events.

For `TransactionCompleted`, two movement entries are created:
- `transfer_out` on the source wallet (`from_wallet_id`)
- `transfer_in` on the destination wallet (`to_wallet_id`)

| Field       | Type     | Notes                                                    |
|-------------|----------|----------------------------------------------------------|
| id          | UUID     | Server-generated movement ID                             |
| wallet_id   | UUID     | Which wallet this movement belongs to                    |
| type        | String   | `deposit`, `withdrawal`, `transfer_in`, `transfer_out`   |
| amount      | Int      | Always positive, type indicates direction                |
| currency    | String   |                                                          |
| reason      | String   |                                                          |
| category    | String   | Only for withdrawals, empty otherwise                    |
| occurred_at | DateTime |                                                          |

## HTTP Endpoint

- **Method:** `GET`
- **Path:** `/wallets/:id/movements`
- **Success:** `200 OK`

```json
{
  "movements": [
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "type": "deposit",
      "amount": 5000,
      "currency": "EUR",
      "reason": "Birthday gift",
      "category": "",
      "occurred_at": "2026-04-25T14:00:00Z"
    },
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "type": "withdrawal",
      "amount": 3000,
      "currency": "EUR",
      "reason": "Lunch",
      "category": "Food",
      "occurred_at": "2026-04-25T15:00:00Z"
    },
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "type": "transfer_out",
      "amount": 1000,
      "currency": "EUR",
      "reason": "Savings to cash",
      "category": "",
      "occurred_at": "2026-04-25T16:00:00Z"
    }
  ]
}
```

- **Errors:**
  - `404` — `wallet_not_found` (no movements found for wallet)

## Error Codes

| Code             | HTTP | Message          |
|------------------|------|------------------|
| wallet_not_found | 404  | wallet not found |
