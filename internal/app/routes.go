package app

import (
	"context"
	"fmt"
	"os"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/getdebts"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/slices/settledebt"

	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/getrecurringmovements"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/recordrecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/slices/stoprecurringmovement"

	pprojections "github.com/jordisalazar/money-management/internal/payments/processes/projections"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/slices/gettransfer"
	"github.com/jordisalazar/money-management/internal/payments/processes/transfer/slices/requesttransfer"

	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/disablewallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/getusermovements"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/getuserwallets"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/getwalletmovements"
	"github.com/jordisalazar/money-management/internal/payments/wallet/slices/withdrawmoney"

	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/voice"
)

// queryStores bundles the projection stores consumed by GET handlers
// and by the voice slice. Lambda mode without AURORA_URL passes a zero
// value (no read models served).
type queryStores struct {
	wallet    wprojections.WalletStore
	movement  wprojections.MovementStore
	debt      dprojections.DebtStore
	recurring rmprojections.RecurringMovementStore
	// processes backs processes_view: every transfer, occurrence and debt
	// settlement with its outcome. It is read by the sweeper as well as by
	// GET handlers, which is why it lives here rather than in a process.
	processes pprojections.ProcessStore

	// bindTx rebinds a store to a transaction so a projection's rows commit
	// alongside its checkpoint. It is nil in memory mode, where the stores are
	// already in memory and there is nothing to bind.
	bindTx *txBinders
}

// txBinders builds Postgres-backed stores against whichever DBTX a projection
// is currently running in.
type txBinders struct {
	wallet    func(database.DBTX) wprojections.WalletStore
	movement  func(database.DBTX) wprojections.MovementStore
	debt      func(database.DBTX) dprojections.DebtStore
	recurring func(database.DBTX) rmprojections.RecurringMovementStore
	processes func(database.DBTX) pprojections.ProcessStore
}

// The *For methods answer "which store should this projection write through?".
//
// With Postgres they build one bound to the transaction in flight, so rows and
// checkpoint commit together. In memory mode there is no transaction and the
// single shared store is the right answer.
func (q queryStores) walletFor(db database.DBTX) wprojections.WalletStore {
	if q.bindTx == nil {
		return q.wallet
	}
	return q.bindTx.wallet(db)
}

func (q queryStores) movementFor(db database.DBTX) wprojections.MovementStore {
	if q.bindTx == nil {
		return q.movement
	}
	return q.bindTx.movement(db)
}

func (q queryStores) debtFor(db database.DBTX) dprojections.DebtStore {
	if q.bindTx == nil {
		return q.debt
	}
	return q.bindTx.debt(db)
}

func (q queryStores) recurringFor(db database.DBTX) rmprojections.RecurringMovementStore {
	if q.bindTx == nil {
		return q.recurring
	}
	return q.bindTx.recurring(db)
}

func (q queryStores) processesFor(db database.DBTX) pprojections.ProcessStore {
	if q.bindTx == nil {
		return q.processes
	}
	return q.bindTx.processes(db)
}

func registerRoutes(
	ctx context.Context,
	r *gin.Engine,
	store eventstore.Store,
	codec *eventstore.Codec,
	voiceOpts voice.ClientOptions,
	transOpts voice.TranscriberOptions,
	mountVoice bool,
	queries queryStores,
	opts Options,
) {
	// Auth slice (register + login) and the UserStorer are gone in
	// phase 4: API Gateway's JWT authorizer validates Cognito tokens at
	// the edge, the middleware reads the sub claim. No backend route
	// owns user creation anymore — Cognito's hosted UI does.

	addwallet.RegisterCommands(r, store, codec)
	depositmoney.RegisterCommands(r, store, codec)
	withdrawmoney.RegisterCommands(r, store, codec)
	disablewallet.RegisterCommands(r, store, codec)
	recorddebt.RegisterCommands(r, store, codec)
	settledebt.RegisterCommands(r, store, codec)
	recordrecurringmovement.RegisterCommands(r, store, codec)
	stoprecurringmovement.RegisterCommands(r, store, codec)

	// The transfer process. POST answers 202 and GET makes the outcome
	// reachable — one is useless without the other.
	requesttransfer.RegisterCommands(r, store, codec)
	gettransfer.RegisterQueries(r, store, codec)

	// refundmoney is deliberately not registered: a refund is only ever
	// issued by a process compensating for a failed step, never by a person.

	if opts.MountQueryRoutes {
		getuserwallets.RegisterQueries(r, queries.wallet)
		getwalletmovements.RegisterQueries(r, queries.movement)
		getusermovements.RegisterQueries(r, queries.wallet, queries.movement)
		getdebts.RegisterQueries(r, queries.debt)
		getrecurringmovements.RegisterQueries(r, queries.recurring)

		if mountVoice {
			// Bedrock backend construction can fail (e.g. AWS config
			// load). Don't fail the whole engine — log + skip mounting.
			if err := voice.RegisterRoutes(ctx, r, store, codec, voiceOpts, transOpts, queries.wallet, queries.debt, queries.recurring); err != nil {
				// We can't log structured here without plumbing logger
				// through; print to stderr is fine for a startup error.
				fmt.Fprintf(os.Stderr, "voice: registration failed, slice disabled: %v\n", err)
			}
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}
