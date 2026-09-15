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
