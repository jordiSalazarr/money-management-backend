// Package rejection classifies the errors a process gets back when it issues a
// command against an aggregate.
//
// Every process needs the same two answers: is this the aggregate refusing on
// a rule (permanent — record a terminal outcome and stop), or is it the
// infrastructure wobbling (transient — retry)? Getting that split wrong is how
// the old reactor lost money: it classified ErrWalletDisabled as permanent,
// which was correct, and then *dropped the record* instead of recording
// anything, which was not.
package rejection

import (
	"errors"

	"github.com/jordisalazar/money-management/internal/payments/wallet/domain"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
)

// Reason codes reuse the API's error_code vocabulary verbatim, so the string a
// process writes into processes_view is the same one the HTTP layer would have
// returned for the same rejection.
const (
	ReasonUnknown = "unknown"
	ReasonTimeout = "timed_out"
)

// walletReasons maps each wallet sentinel to its error_code. A sentinel that
// is not in here is not a domain rejection, and is therefore retried.
var walletReasons = []struct {
	err  error
	code string
}{
	{wallet.ErrInsufficientFunds, "insufficient_funds"},
	{wallet.ErrCurrencyMismatch, "currency_mismatch"},
	{wallet.ErrWalletDisabled, "wallet_disabled"},
	{wallet.ErrInvalidAmount, "invalid_amount"},
	{wallet.ErrInvalidDepositCategory, "invalid_category"},
	{wallet.ErrInvalidWithdrawCategory, "invalid_category"},
	{wallet.ErrMissingUserID, "missing_user_id"},
	{eventstore.ErrAggregateNotFound, "wallet_not_found"},
}

// Classify reports whether err is an aggregate refusing on a rule, and the
// snake_case code to record if so.
//
// Anything unrecognised is treated as transient. That bias is deliberate:
// retrying a transient fault costs a redelivery, which idempotency keys make
// free, whereas mistaking a transient fault for a permanent one closes a
// transfer that could have succeeded.
func Classify(err error) (code string, permanent bool) {
	if err == nil {
		return "", false
	}
	for _, r := range walletReasons {
		if errors.Is(err, r.err) {
			return r.code, true
		}
	}
	return ReasonUnknown, false
}

// IsAlreadyProcessed reports whether a command was a no-op because its
// idempotency key had already been applied.
//
// This is a *success*, not a failure: it means the effect the process wanted
// is already in place, which is exactly what a redelivery should look like.
func IsAlreadyProcessed(err error) bool {
	return errors.Is(err, wallet.ErrAlreadyProcessed)
}
