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

	_ "github.com/jordisalazar/money-management/docs"
	"github.com/jordisalazar/money-management/internal/app"
	"github.com/jordisalazar/money-management/pkg/logger"
)

func main() {
	log := logger.New()

	r, cleanup, err := app.BuildEngine(context.Background(), log, app.Options{
		StartBackgroundJobs: true,
		MountQueryRoutes:    true,
		DefaultStore:        app.StoreModePostgres,
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
