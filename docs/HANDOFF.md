# Where the KurrentDB migration got to

Written 2026-09-15, mid-session, before a machine restart. Everything below is
committed; nothing is in a dirty working tree.

## Resume in one line

Docker Desktop's memory change had not reached the daemon (it still reported
1.9 GB after 6 days' uptime). After the restart, check it, then run the cluster:

```bash
docker info | grep "Total Memory"          # want ~4 GB, not 1.9
cd money-management-k8s && make up && make seed
```

That is the only thing outstanding in phases 1–6 apart from one question below.

## Branches

| repo | branch | base |
|---|---|---|
| `backend` | `feat/kurrent-phase-1` | `main` at `8343f19` |
| `web` | `fix/transfer-202-contract` | `main` at `670e066` |
| `money-management-k8s` | `main` (new repo) | — |

`main` is untouched in both existing repos. Each branch starts with a baseline
commit holding the work that was already uncommitted when this began, so the
migration is a separable diff on top of it.

## State by phase

| phase | status |
|---|---|
| 1 — codec/upcaster seam, processes, sweeper | done, verified |
| 2 — KurrentDB adapter | done, verified against a real container |
| 3 — per-projection checkpoints, persistent subscription | done, verified |
| 4 — containers, kind, Helm | **written; full run blocked on Docker memory** |
| 4.5 — decommission AWS | code done; `terraform destroy` **not run, needs your say-so** |
| 5 — JWKS auth | done, verified |
| 6 — lag metrics + replay | done, verified |

Full detail is in `backend/docs/k8s-migration-plan.md`, which was kept in step
with the code throughout — where the plan and the implementation disagreed, the
plan was corrected and the correction recorded.

## The one open question

**`terraform destroy` in `money-management-infra/envs/dev`.** Not run, and not
something to infer from "decommission AWS": it destroys live infrastructure
irreversibly. When you want it, say so and the JSONL backup of the DynamoDB
events table comes first, per §8 of the plan.

Everything else in 4.5 is done — `cmd/lambda`, the DynamoDB store,
`aws-lambda-go` and the DDB SDK are gone. What remains of AWS is Bedrock and
Transcribe, which is exactly the `voice` slice. That slice still has no
non-AWS fallback for Transcribe, which §10 of the plan lists as open.

## Verification state

Backend: `go build`, `go vet`, 27 packages green, 0 races. The KurrentDB
suites (`make test-kurrent`) pass against a real container — **stop any running
server first**, or its reactor drives the same transfers the tests do and they
collide on the stream.

Web: `tsc` clean, `npm run build` clean.

Chart: `helm lint` clean, templates to 16 objects. **Never actually run to
completion** — see below.

## What running things found that reading them did not

Worth knowing, because it is the argument for finishing the cluster run rather
than trusting the chart:

- A `pre-install` hook cannot migrate a database that ships in the same chart.
  Hooks run before *every* chart resource, so the Job waited for a Postgres
  that did not exist yet and the install hung.
- `cmd/migrate` and `cmd/recurring-cron` were still Lambda binaries and
  crash-looped in Kubernetes. Phase 3's rewrite had covered the projector and
  reactor and missed these two.
- `wallets_projection` never handled `MoneyRefunded`, so after a compensated
  transfer the projected balance sat permanently below the aggregate — and the
  projected number is the one a person sees.
- Migrations `008` and `009` were missing their `-- +migrate Up` annotations,
  so `009` had been dead since phase 1. Nothing ran migrations, so nothing
  noticed.
- `MemoryCheckpoints` raced under five concurrent runners. Every single-runner
  test passed and `go test -race` never saw it.

None of these were visible to a build, a lint, or a unit test.

## Things to be careful of

**Stray servers.** `go run ./cmd/server` execs a binary that `pkill -f
"cmd/server"` does not match. Leftovers stay subscribed to `$all` and drive the
same transfers as the tests, which looks exactly like a flaky test. Kill with
`pkill -f "/server$"`.

**A shared dev KurrentDB accumulates junk.** `make dev-reset` only clears the
projections; `make dev-nuke` takes the event store with it. Wiping projections
alone leaves their checkpoints intact, so nothing replays and the read models
stay blank — worse than not resetting.

## Local commands

```bash
# backend, no containers at all
make -C backend run

# backend on KurrentDB + Postgres
make -C backend dev-stack

# contract + saga tests against a real KurrentDB
make -C backend test-kurrent

# rebuild ONE read model
make -C backend replay P=movements_projector

# the whole stack on kind
cd money-management-k8s && make up && make seed
```
