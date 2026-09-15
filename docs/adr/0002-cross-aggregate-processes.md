# Cross-aggregate processes

KurrentDB has no multi-stream transaction, so anything touching two aggregates must become a
multi-step process that can fail partway. We decided every such flow in this codebase gets
**one uniform shape**: a request event, zero or more intermediate steps, and exactly one
terminal outcome — and that the past tense is reserved for facts that are already true.

```
Transfer    TransferRequested          → TransferDebited
                                       → TransferCompleted | TransferFailed | TransferStuck
Occurrence  MovementDue                → MovementExecuted  | MovementSkipped
Debt        DebtSettlementRequested    → DebtSettled       | DebtSettlementFailed
```

## Why the renames

`transfermoney/command_handler.go` called `Create()` and `Complete()` on consecutive lines
inside one `AppendMulti`, so `Transaction` was a receipt, not a process — `StatusCreated` had
never been observed and `TransactionCreated` was never even registered. Under a saga it
becomes a state machine with observable intermediate states, including states where no money
moved. Meanwhile the HTTP route was already `POST /transfers`. `Transaction` also collides
with "database transaction", which is the precise mechanism this migration removes. The
concept is now **Transfer** everywhere.

`RecurringMovementExecuted` and `DebtSettled` were worse than badly named: both were appended
*before* the reactor attempted the corresponding wallet command. When that command returned
`ErrInsufficientFunds`, `reactor.isPermanent()` classified it permanent and the record was
dropped with a `log.Warn`. The result was a standing order permanently marked executed for a
month in which no money moved, and a debt reading settled without payment. Neither needed a
race or an outage — only a month where the balance was short. The fact is now asserted only
once it is true.

## Failure handling

- **Business rejection before money moves** → terminal failure event, no compensation needed.
- **Business rejection after money moves** → compensate, then terminal failure event.
- **Compensation rejected by the domain** → `wallet.Refund` exists precisely so the common
  case cannot happen: returning money you already took out is not new money arriving, so it
  ignores the disabled-wallet and category checks. If a compensation still fails, the process
  writes `TransferStuck` and parks. Nothing is ever lost silently.
- **Transient infrastructure failure** → `Nack(Retry)`.
- **No progress at all** — reactor down, message parked, event never routed — is invisible to
  every mechanism above, so a cron sweeps non-terminal processes past a threshold and
  re-drives them from their last recorded state. Re-driving is safe because every step carries
  a stable idempotency key. After K attempts it gives up, and what it writes depends on
  whether money moved: no money moved → failed; money moved → stuck.

Invariant: **failed ⇒ no money moved. stuck ⇒ money is in limbo, and it is written down.**

## Stream identity

A Transfer is one stream, `transfer-<id>`. A debt settlement is one-shot and lives on
`debt-<id>`. A Recurring Movement fires forever, so its stream holds only the instruction and
each firing gets its own stream, `occurrence-<rmID>-<yyyy-mm-dd>`.

That last split is not only about stream size. The stream name is derived from the recurring
movement and the date, so appending `MovementDue` at `ExpectedRevision.NoStream` fails with
`WrongExpectedVersion` if the date already fired. **The stream identity is the idempotency
key** — no dedup table, no key to remember to pass. It also keeps `recurringmovement-<id>`
bounded, removes `expectedVersion` contention between occurrences catching up after an
outage, and gives the sweeper a `$ce-occurrence` category to query.

## Consequences

- Two kinds of idempotency, both required. The wallet-level keys (`<txID>:debit`,
  `<txID>:credit`, `<txID>:refund`) stop money moving twice. A process-level state check stops
  the reactor livelocking: on `WrongExpectedVersion` it reloads the process, and if the step is
  already recorded it acks rather than retrying forever.
- `MovementSkipped` is user-facing. "Your standing order was skipped this month — insufficient
  funds" is what a bank tells you, and the read model and UI must be able to say it.
- Transfers become eventually consistent: `POST /transfers` returns 202 with a pollable id.
- The balance precheck at the API boundary is advisory. It checks balance only, so it does not
  catch a currency mismatch between the two wallets — the authoritative check is inside
  `wallet.Withdraw` when the reactor runs it.

## Where processes live

Process managers go in `internal/payments/processes/{transfer,occurrence,debtsettlement}/`,
a sibling of the aggregate contexts (`wallet/`, `debt/`, `recurringmovement/`). Each owns its
process aggregate, its reactor, its slices and its projection.

The folder exists to name an exception to the rule in `CLAUDE.md` that a slice owns
everything it needs. A process manager's job is to issue commands against aggregates it does
not own, so it is inherently cross-slice — `settledebt/reactor.go` already imported
`wallet/slices/depositmoney` and `wallet/slices/withdrawmoney` before any of this. With three
processes that becomes roughly nine deliberate edges. Left undocumented, someone eventually
"fixes" it by introducing a command bus, which the same file also forbids.

**A process may import any slice. No slice may import a process.** The arrow points one way.

The rejected alternative was a `wallet.Commands` port that processes depend on and slices
implement, inverting the dependency. It is more principled, but it means an interface with a
single implementation and a fake to test against — both of which `CLAUDE.md` rules out except
at a genuine architectural boundary, and this is not one.
