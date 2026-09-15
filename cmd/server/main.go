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
	"os"
	"os/signal"
	"syscall"

	_ "github.com/jordisalazar/money-management/docs"
	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func main() {
	log := logger.New()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	r, cleanup, err := app.BuildEngine(ctx, log, app.Options{
		MountQueryRoutes: true,
		DefaultStore:     app.StoreModeMemory,
		RunAsync:         true,
	})
	if err != nil {
		log.Error("failed to build engine", "error", err)
		os.Exit(1)
	}
	defer cleanup()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8019"
	}

	log.Info("starting server", "port", port)
	if err := r.Run(":" + port); err != nil {
		log.Error("server failed", "error", err)
	}
}
