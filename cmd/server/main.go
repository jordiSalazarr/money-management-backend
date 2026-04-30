// @title          Money Management API
// @version        1.0
// @description    Personal money management API. Manage wallets, deposits, withdrawals, and transfers. All monetary amounts are in integer cents.
// @host           localhost:8019
// @BasePath       /
// @schemes        http
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/jordisalazar/money-management/auth"
	"github.com/jordisalazar/money-management/auth/login"
	"github.com/jordisalazar/money-management/auth/register"
	"github.com/jordisalazar/money-management/debt"
	"github.com/jordisalazar/money-management/debt/getdebts"
	dprojections "github.com/jordisalazar/money-management/debt/projections"
	"github.com/jordisalazar/money-management/debt/recorddebt"
	"github.com/jordisalazar/money-management/debt/settledebt"
	_ "github.com/jordisalazar/money-management/docs"
	"github.com/jordisalazar/money-management/pkg/logger"
	"github.com/jordisalazar/money-management/recurringmovement"
	"github.com/jordisalazar/money-management/recurringmovement/executerecurringmovement"
	rmprojections "github.com/jordisalazar/money-management/recurringmovement/projections"
	"github.com/jordisalazar/money-management/recurringmovement/recordrecurringmovement"
	"github.com/jordisalazar/money-management/shared/database"
	"github.com/jordisalazar/money-management/shared/eventstore"
	"github.com/jordisalazar/money-management/shared/middleware"
	"github.com/jordisalazar/money-management/transaction"
	txprojections "github.com/jordisalazar/money-management/transaction/projections"
	"github.com/jordisalazar/money-management/transaction/transfermoney"
	"github.com/jordisalazar/money-management/wallet"
	"github.com/jordisalazar/money-management/wallet/addwallet"
	"github.com/jordisalazar/money-management/wallet/depositmoney"
	"github.com/jordisalazar/money-management/wallet/getusermovements"
	"github.com/jordisalazar/money-management/wallet/getuserwallets"
	"github.com/jordisalazar/money-management/wallet/getwalletmovements"
	wprojections "github.com/jordisalazar/money-management/wallet/projections"
	"github.com/jordisalazar/money-management/wallet/withdrawmoney"

	"github.com/jordisalazar/money-management/shared/outbox"
)

func main() {
	log := logger.New()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://money:money@localhost:5432/money_management?sslmode=disable"
	}

	// Run database migrations
	applied, err := database.Migrate(databaseURL)
	if err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}
	if applied > 0 {
		log.Info("migrations applied", "count", applied)
	}

	// Create connection pool
	pool, err := database.NewPool(context.Background(), databaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	store := eventstore.NewPostgresStore(pool)
	userStore := auth.NewPostgresUserStore(pool)

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "dev-secret-do-not-use-in-production"
	}

	r := gin.New()
	r.Use(middleware.CORS())
	r.Use(middleware.Correlation())
	r.Use(middleware.Logging(log))
	r.Use(middleware.Auth(jwtSecret))

	// Register domain event types for deserialization
	store.RegisterEvent(wallet.EventTypeWalletAdded, func() any { return &wallet.WalletAdded{} })
	store.RegisterEvent(wallet.EventTypeMoneyDeposited, func() any { return &wallet.MoneyDeposited{} })
	store.RegisterEvent(wallet.EventTypeMoneyWithdrawn, func() any { return &wallet.MoneyWithdrawn{} })
	store.RegisterEvent(transaction.EventTypeTransactionCompleted, func() any { return &transaction.TransactionCompleted{} })
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })

	// Projection stores — Postgres-backed read models
	walletStore := wprojections.NewPostgresWalletStore(pool)
	movementStore := wprojections.NewPostgresMovementStore(pool)
	transactionStore := txprojections.NewPostgresTransactionStore(pool)
	debtStore := dprojections.NewPostgresDebtStore(pool)
	recurringMovementStore := rmprojections.NewPostgresRecurringMovementStore(pool)

	// Projectors — subscribe to event store and write to projection stores
	walletsProjector := wprojections.NewWalletsProjector(walletStore)
	movementsProjector := wprojections.NewMovementsProjector(movementStore)
	transactionsProjector := txprojections.NewTransactionsProjector(transactionStore)
	debtsProjector := dprojections.NewDebtsProjector(debtStore)
	store.Subscribe(walletsProjector)
	store.Subscribe(movementsProjector)
	store.Subscribe(transactionsProjector)
	store.Subscribe(debtsProjector)
	recurringMovementsProjector := rmprojections.NewRecurringMovementsProjector(recurringMovementStore)
	store.Subscribe(recurringMovementsProjector)

	// Replay existing events to hydrate projections on startup
	if err := store.ReplayAll(context.Background()); err != nil {
		slog.Error("failed to replay events", "error", err)
		os.Exit(1)
	}
	log.Info("projections hydrated from event store")

	// Auth routes
	register.RegisterRoutes(r, userStore)
	login.RegisterRoutes(r, userStore, jwtSecret)

	// Register slices here:
	addwallet.RegisterCommands(r, store)
	depositmoney.RegisterCommands(r, store)
	withdrawmoney.RegisterCommands(r, store)
	transfermoney.RegisterCommands(r, store)
	recorddebt.RegisterCommands(r, store)
	settledebt.RegisterCommands(r, store)
	recordrecurringmovement.RegisterCommands(r, store)

	// Register queries — pass stores, not projectors
	getuserwallets.RegisterQueries(r, walletStore)
	getwalletmovements.RegisterQueries(r, movementStore)
	getusermovements.RegisterQueries(r, walletStore, movementStore)
	getdebts.RegisterQueries(r, debtStore)

	// Outbox poller — routes by event_type to the appropriate reactor
	debtReactor := settledebt.NewReactorHandler(store)
	recurringReactor := executerecurringmovement.NewReactorHandler(store)
	outboxRouter := func(ctx context.Context, entry outbox.Entry) error {
		switch entry.EventType {
		case debt.EventTypeDebtSettled:
			return debtReactor(ctx, entry)
		case recurringmovement.EventTypeRecurringMovementExecuted:
			return recurringReactor(ctx, entry)
		default:
			slog.Warn("outbox: unknown event type", "event_type", entry.EventType)
			return nil
		}
	}
	poller := outbox.NewPoller(pool, outboxRouter, 2*time.Second)
	go poller.Start(context.Background())
	log.Info("outbox poller started", "interval", "2s")

	// Recurring movement processor — checks for due items every hour
	rmProcessor := executerecurringmovement.NewProcessor(pool, store, 10*time.Second)
	go rmProcessor.Start(context.Background())
	log.Info("recurring movement processor started", "interval", "1h")

	// Swagger UI
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8019"
	}

	log.Info("starting server", "port", port)
	if err := r.Run(":" + port); err != nil {
		log.Error("server failed", "error", err)
	}
}
