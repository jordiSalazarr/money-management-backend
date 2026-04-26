# GetUserWallets Slice

## Aggregate

No aggregate — this is a read-only slice.

## Query: GetUserWallets

| Field   | Type | Required | Notes              |
|---------|------|----------|--------------------|
| user_id | UUID | yes      | From query string  |

## Projection: Wallets (existing)

Uses the existing `WalletsProjector.ListByUserID()` from `addwallet/projection.go`. No new projection needed.

## HTTP Endpoint

- **Method:** `GET`
- **Path:** `/wallets?user_id=...`
- **Success:** `200 OK`

```json
{
  "wallets": [
    {
      "id": "uuid",
      "user_id": "uuid",
      "name": "My Savings",
      "description": "Main savings",
      "current_balance": 50000,
      "currency": "EUR",
      "category": "Saving",
      "created_at": "2026-04-25T12:00:00Z",
      "last_movement_at": "2026-04-25T14:00:00Z"
    }
  ]
}
```

- **Errors:**
  - `400` — `missing_user_id`

## Error Codes

| Code            | HTTP | Message             |
|-----------------|------|---------------------|
| missing_user_id | 400  | user_id is required |
