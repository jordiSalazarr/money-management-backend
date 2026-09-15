# Payments

Personal money management: the wallets a person keeps, the money that moves in and out of
them, and the money they owe or are owed.

## Wallets and money

**Wallet**:
A named pot of money belonging to one person, in one currency, with a balance.
_Avoid_: Account, pot, fund

**Balance**:
The amount of money currently in a Wallet, always in integer cents.

**Deposit**:
Money entering a single Wallet.
_Avoid_: Credit, income, top-up

**Withdrawal**:
Money leaving a single Wallet.
_Avoid_: Debit, expense, spend

**Refund**:
Money returned to a Wallet to undo a Withdrawal that should not have stood. A Refund is not
a Deposit: it puts back money that was already the Wallet's, so it is not subject to the
rules that govern new money arriving, and a disabled Wallet still accepts one.

**Movement**:
A single Deposit, Withdrawal or Refund as it appears in a Wallet's history. The unit a
person reads when they look at what happened to their money.
_Avoid_: Entry, line item, transaction

**Debt**:
Money a person owes to someone, or is owed by someone, tracked against a Wallet until it is
settled.

## Things that happen over time

**Process**:
Something a person sets in motion that takes more than one step across more than one Wallet
or Debt, and can end badly. Every Process is requested, then reaches exactly one outcome.
Nothing in this context changes two Wallets at once without being a Process.

**Transfer**:
A person's request to move money from one of their Wallets to another. A Transfer is an
attempt, not an outcome: it is requested, then either completes or fails, and a failed
Transfer moved no money. It is the thing a person initiates and whose status they check.
_Avoid_: Transaction, payment, transfer transaction

**Recurring Movement**:
A standing instruction that a Movement should happen on a schedule, until it is stopped. The
instruction itself never moves money — it only says what should happen and when.
_Avoid_: Subscription, standing order, scheduled transaction

**Occurrence**:
One firing of a Recurring Movement, for one date. An Occurrence either produces a Movement
or is skipped, and it is identified by its Recurring Movement and its date — never twice for
the same date.

**Skipped**:
The outcome of an Occurrence that could not produce its Movement, and the reason why. A
person's standing order being skipped for a month is an ordinary, reportable event, not an
error.

**Stuck**:
The state of a Process that moved money and then could not put it back. Money is genuinely
in limbo and a person must decide what happens next. Distinct from failed: a failed Process
moved no money at all.

## Naming rule for events

An event is only written in the past tense once the thing it names is true. A Process
therefore emits a request first (`TransferRequested`, `MovementDue`,
`DebtSettlementRequested`) and asserts the fact only at the end (`TransferCompleted`,
`MovementExecuted`, `DebtSettled`). See
[ADR 0002](./docs/adr/0002-cross-aggregate-processes.md).

## Retired terms

**Transaction** is deliberately not part of this language. It previously named what is now a
Transfer, while the API already said `/transfers`, and it collided with "database
transaction" — the exact mechanism the KurrentDB migration removes.
