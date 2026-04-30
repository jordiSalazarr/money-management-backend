# StopRecurringMovement Slice

## Aggregate

- **Name:** RecurringMovement
- **ID:** `recurring_movement_id` (UUID)
- **Type:** `recurring_movement`

This slice operates on the existing RecurringMovement aggregate. It adds a `Stop()` method.

## Command: StopRecurringMovement

| Field                  | Type     | Required | Notes                          |
|------------------------|----------|----------|--------------------------------|
| recurring_movement_id  | UUID     | yes      | From URL path param            |
| stopped_by             | UUID     | yes      | User ID (from auth context)    |
| reason                 | String   | no       | Why the user is stopping it    |

`stopped_at` is not part of the command — it is server-generated (UTC) inside the aggregate method.

### Invariants

1. Recurring movement must exist (loaded from event store)
2. Recurring movement must be active (`active = true`)
3. `stopped_by` must match the aggregate's `user_id` (only the owner can stop it)

## Event: RecurringMovementStopped

| Field                  | Type     | Notes                          |
|------------------------|----------|--------------------------------|
| recurring_movement_id  | UUID     | Aggregate ID                   |
| stopped_by             | UUID     | User who stopped it            |
| reason                 | String   | Empty string if not provided   |
| stopped_at             | DateTime | Server-generated (UTC)         |

## Aggregate State Transition

`Apply(RecurringMovementStopped)`:
- `a.active = false`

No other state changes needed. The aggregate is already `active = false`, which makes `Execute()` return `ErrNotActive` and `Stop()` return `ErrNotActive` (idempotent guard).

## Projection: RecurringMovements (existing)

`RecurringMovementStopped` updates the existing RecurringMovements read model:

- Set `active = false`

No new projection table or migration needed. The existing `recurring_movements_view` already has the `active` column.

## Processor Interaction

No changes needed. The processor already queries `WHERE active = true`, so stopped movements are automatically excluded from execution.

## HTTP Endpoint

### Stop a recurring movement

- **Method:** `POST`
- **Path:** `/recurring-movements/:id/stop`
- **Request body:**

```json
{
  "reason": "No longer needed"
}
```

`reason` may be omitted or empty.

- **Success:** `200 OK`

```json
{
  "recurring_movement_id": "uuid",
  "stopped_at": "2026-04-30T12:00:00Z"
}
```

- **Errors:**
  - `404` — `recurring_movement_not_found` (aggregate not found in event store)
  - `400` — `not_active` (already stopped or expired)
  - `403` — `not_owner` (stopped_by does not match aggregate user_id)

## Error Codes

| Code                          | HTTP | Message                                  |
|-------------------------------|------|------------------------------------------|
| recurring_movement_not_found  | 404  | recurring movement not found             |
| not_active                    | 400  | recurring movement is not active         |
| not_owner                     | 403  | only the owner can stop this movement    |
