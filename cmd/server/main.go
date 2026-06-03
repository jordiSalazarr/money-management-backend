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

	"github.com/jordisalazar/money-management/internal/auth"
	"github.com/jordisalazar/money-management/internal/auth/login"
	"github.com/jordisalazar/money-management/internal/auth/register"
	"github.com/jordisalazar/money-management/internal/payments/debt"
	"github.com/jordisalazar/money-management/internal/payments/debt/getdebts"
	dprojections "github.com/jordisalazar/money-management/internal/payments/debt/projections"
	"github.com/jordisalazar/money-management/internal/payments/debt/recorddebt"
	"github.com/jordisalazar/money-management/internal/payments/debt/settledebt"
	_ "github.com/jordisalazar/money-management/docs"
	"github.com/jordisalazar/money-management/pkg/logger"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/executerecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/getrecurringmovements"
	rmprojections "github.com/jordisalazar/money-management/internal/payments/recurringmovement/projections"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/recordrecurringmovement"
	"github.com/jordisalazar/money-management/internal/payments/recurringmovement/stoprecurringmovement"
	"github.com/jordisalazar/money-management/internal/shared/database"
	"github.com/jordisalazar/money-management/internal/shared/eventstore"
	"github.com/jordisalazar/money-management/internal/shared/middleware"
	"github.com/jordisalazar/money-management/internal/payments/transaction"
	txprojections "github.com/jordisalazar/money-management/internal/payments/transaction/projections"
	"github.com/jordisalazar/money-management/internal/payments/transaction/transfermoney"
	"github.com/jordisalazar/money-management/internal/voice"
	"github.com/jordisalazar/money-management/internal/payments/wallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/addwallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/depositmoney"
	"github.com/jordisalazar/money-management/internal/payments/wallet/disablewallet"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getusermovements"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getuserwallets"
	"github.com/jordisalazar/money-management/internal/payments/wallet/getwalletmovements"
	wprojections "github.com/jordisalazar/money-management/internal/payments/wallet/projections"
	"github.com/jordisalazar/money-management/internal/payments/wallet/withdrawmoney"

	"github.com/jordisalazar/money-management/internal/shared/outbox"
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
	store.RegisterEvent(wallet.EventTypeWalletDisabled, func() any { return &wallet.WalletDisabled{} })
	store.RegisterEvent(transaction.EventTypeTransactionCompleted, func() any { return &transaction.TransactionCompleted{} })
	store.RegisterEvent(debt.EventTypeDebtRecorded, func() any { return &debt.DebtRecorded{} })
	store.RegisterEvent(debt.EventTypeDebtSettled, func() any { return &debt.DebtSettled{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementRegistered, func() any { return &recurringmovement.RecurringMovementRegistered{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementExecuted, func() any { return &recurringmovement.RecurringMovementExecuted{} })
	store.RegisterEvent(recurringmovement.EventTypeRecurringMovementStopped, func() any { return &recurringmovement.RecurringMovementStopped{} })

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
	disablewallet.RegisterCommands(r, store)
	transfermoney.RegisterCommands(r, store)
	recorddebt.RegisterCommands(r, store)
	settledebt.RegisterCommands(r, store)
	recordrecurringmovement.RegisterCommands(r, store)
	stoprecurringmovement.RegisterCommands(r, store)

	// Register queries — pass stores, not projectors
	getuserwallets.RegisterQueries(r, walletStore)
	getwalletmovements.RegisterQueries(r, movementStore)
	getusermovements.RegisterQueries(r, walletStore, movementStore)
	getdebts.RegisterQueries(r, debtStore)
	getrecurringmovements.RegisterQueries(r, recurringMovementStore)

	// Voice assistant
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicKey != "" {
		voice.RegisterRoutes(r, store, anthropicKey, walletStore, debtStore, recurringMovementStore)
		log.Info("voice assistant enabled")
	} else {
		log.Info("voice assistant disabled (ANTHROPIC_API_KEY not set)")
	}

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
