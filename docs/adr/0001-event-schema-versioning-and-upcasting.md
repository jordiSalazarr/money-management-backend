# Event schema versioning and upcasting

Moving to KurrentDB, we want event evolution handled from the first commit rather than
retrofitted. We decided: the **schema version lives in KurrentDB event metadata**
(`schema_version`), never in the event type name, which stays stable forever; a single
`eventstore.Codec` owns both the type registry and the upcaster chains and is the only
place raw payloads are decoded; upcasters are **typed Go functions** over frozen historical
structs, wired in via a generic `Typed[From,To]` adapter; and the version is **bumped on
every shape change**, including additive ones that need no upcaster.

## Considered options

**Version in the event type name** (`MoneyDeposited.v2`) is the dominant EventStoreDB
convention and is self-describing in the UI. Rejected because every routing table in this
codebase is keyed by the bare type string — `registerDomainEvents`, the reactor's
`switch eventType`, the projector dispatcher — so each version bump would either fragment
those tables or require a suffix-stripping helper, which is metadata-versioning with extra
steps. It would also split `$et-MoneyDeposited` into a projection per version.

**JSON→JSON upcasters** avoid accumulating frozen structs. Rejected because Go makes
additive and subtractive changes need no upcaster at all — `json.Unmarshal` zeroes absent
fields and ignores unknown ones. The only changes that *do* need an upcaster are renames,
type changes, non-zero defaults, splits/merges, and semantic reinterpretations: rare, and
each one a genuine domain translation where a mistyped snake_case key compiles fine and
silently corrupts every replayed event. Typed upcasters put the compiler on that.

**Bumping only when an upcaster is required** keeps the chain dense. Rejected because it
makes additive changes invisible: an event written before a field existed becomes
indistinguishable from one where the user left it blank, so the decision to backfill a
different default later is unrecoverable. Bumping always costs nothing at write time and
keeps that door open; the chain simply treats a missing step as identity.

## Consequences

- Five duplicated private `unmarshalEvent` switches (`depositmoney`, `withdrawmoney`,
  `disablewallet`, `stoprecurringmovement`, `executerecurringmovement`) are deleted in
  favour of the codec. One of them was already missing a case — `depositmoney` did not
  handle `MoneyWithdrawn`, so rehydration silently skipped withdrawals and the deposit
  response returned an inflated balance.
- The codec hard-errors on an event type unknown to it. A type that is registered but
  simply not handled by a given projector or reactor is skipped silently — that is normal
  fan-out, not a fault.
- `expectedVersion` is taken from the raw stream revision, never from the count of decoded
  events.
- Each event type carries `testdata/<Type>.v<N>.json` golden fixtures. The current
  version's fixture is a tripwire that fails when the struct changes without a bump; the
  historical fixtures are the inputs to upcaster tests.
- Upcasters are 1→1. Splits, merges, and changes requiring absent information get a new
  event type instead. Retiring a type means ceasing to emit it, never ceasing to
  understand it: its factory stays registered and its `Apply()` case stays forever.
- The upcaster registry ships **empty**. The seam exists from day 1; the store starts
  empty, so there is nothing to upcast yet.
