# GetUserMovements Slice

## Aggregate

No aggregate — this is a read-only slice that composes existing projections.

## Query: GetUserMovements

No input fields — the authenticated user ID comes from the JWT context.

## Projection: none (new)

No new projection. This slice composes two existing projections:
- `WalletsProjector` (from `addwallet`) — to get the user's wallet IDs and names
- `MovementsProjector` (from `getwalletmovements`) — to get movements per wallet

The handler iterates over the user's wallets, collects all movements, enriches each with `wallet_name`, and sorts by `occurred_at` descending (most recent first).

## Read Model (response)

| Field       | Type     | Notes                                                    |
|-------------|----------|----------------------------------------------------------|
| id          | UUID     | Movement ID (from MovementsProjector)                    |
| wallet_id   | UUID     | Which wallet this movement belongs to                    |
| wallet_name | String   | Enriched from WalletsProjector                           |
| type        | String   | `deposit`, `withdrawal`, `transfer_in`, `transfer_out`   |
| amount      | Int      | Always positive, type indicates direction                |
| currency    | String   |                                                          |
| reason      | String   |                                                          |
| category    | String   | Only for withdrawals, empty otherwise                    |
| occurred_at | DateTime |                                                          |

## HTTP Endpoint

- **Method:** `GET`
- **Path:** `/movements`
- **Success:** `200 OK`

```json
{
  "movements": [
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "wallet_name": "Savings",
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
      "wallet_name": "Cash",
      "type": "withdrawal",
      "amount": 3000,
      "currency": "EUR",
      "reason": "Lunch",
      "category": "Food",
      "occurred_at": "2026-04-25T13:00:00Z"
    },
    {
      "id": "uuid",
      "wallet_id": "uuid",
      "wallet_name": "Savings",
      "type": "transfer_out",
      "amount": 1000,
      "currency": "EUR",
      "reason": "Savings to cash",
      "category": "",
      "occurred_at": "2026-04-25T12:00:00Z"
    }
  ]
}
```

## Error Codes

None — returns empty array if user has no wallets or movements.
