# One subscription and checkpoint per projection

Each projection gets its own catch-up subscription on `$all` and its own row in
`projection_checkpoints`, keyed by subscription name, committing its rows and its checkpoint
in a single transaction. We are **not** running one subscription that advances every
projection in lockstep.

The reason is rebuild cost. [ADR 0001](./0001-event-schema-versioning-and-upcasting.md)
commits us to upcasting, and adding an upcaster means replaying the projections it affects.
With independent subscriptions that is: truncate one table, delete one checkpoint row,
restart — and it catches up alone while the other four keep serving. With a shared
checkpoint, changing one upcaster means truncating and replaying everything, and a single
failing projection stalls all of the others behind it.

The cost is that views sit at slightly different positions, so a screen showing a wallet
balance alongside its recent movements can briefly show a balance that includes a movement not
yet in the list. It self-corrects within milliseconds and no invariant depends on the two
agreeing.

Note this supersedes the topology in §2 of `k8s-migration-plan.md`, which describes a single
projector checkpoint — though the migration it specifies,
`008_create_projection_checkpoints.sql`, was already keyed by `subscription_name`.

## Consequences

- Projection stores take a `DBTX` interface (`Exec`/`Query`/`QueryRow`) satisfied by both
  `*pgxpool.Pool` and `pgx.Tx`, so rows and checkpoint commit together.
- The projector Deployment stays at `replicas: 1` with `strategy: Recreate`. Independent
  checkpoints do not make a projection safe to run twice concurrently.
- Lag is now per-projection: the liveness signal is `$all` head position minus the *oldest*
  stored checkpoint, and the metric should be labelled by subscription name.
- The reactor is unaffected — it stays on a persistent subscription with server-side
  checkpointing, `Ack` / `Nack(Park)` / `Nack(Retry)`.
