package app

import (
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/jordisalazar/money-management/internal/payments/debt/getdebts"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/debt/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/settledebt"

	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/getrecurringmovements"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/recordrecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/stoprecurringmovement"

	"github.com/jordisalazar/money-management/internal/payments/transaction/transfermoney"

	"github.com/jordisalazar/money-management/internal/payments/wallet/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/disablewallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getusermovements"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getuserwallets"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getwalletmovements"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet/withdrawmoney"

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
}

func registerRoutes(
	r *gin.Engine,
	store eventstore.Store,
	anthropicKey string,
	queries queryStores,
	opts Options,
) {
	// Auth slice (register + login) and the UserStorer are gone in
	// phase 4: API Gateway's JWT authorizer validates Cognito tokens at
	// the edge, the middleware reads the sub claim. No backend route
	// owns user creation anymore — Cognito's hosted UI does.

	addwallet.RegisterCommands(r, store)
	depositmoney.RegisterCommands(r, store)
	withdrawmoney.RegisterCommands(r, store)
	disablewallet.RegisterCommands(r, store)
	transfermoney.RegisterCommands(r, store)
	recorddebt.RegisterCommands(r, store)
	settledebt.RegisterCommands(r, store)
	recordrecurringmovement.RegisterCommands(r, store)
	stoprecurringmovement.RegisterCommands(r, store)

	if opts.MountQueryRoutes {
		getuserwallets.RegisterQueries(r, queries.wallet)
		getwalletmovements.RegisterQueries(r, queries.movement)
		getusermovements.RegisterQueries(r, queries.wallet, queries.movement)
		getdebts.RegisterQueries(r, queries.debt)
		getrecurringmovements.RegisterQueries(r, queries.recurring)

		if anthropicKey != "" {
			voice.RegisterRoutes(r, store, anthropicKey, queries.wallet, queries.debt, queries.recurring)
		}
	}

	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}
