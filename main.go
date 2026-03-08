package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"ReZeroTsu/internal/bot"
	"ReZeroTsu/internal/config"
	"ReZeroTsu/internal/database"
	"ReZeroTsu/internal/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		logger.For("main").Error("config load failed", "err", err)
		os.Exit(1)
	}
	logger.Init(cfg.LogLevel, cfg.LogFormat)
	logger.PrintAlways("ZeroTsu started")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// On SIGINT/SIGTERM cancel ctx so bot and loops exit.
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
		<-sig
		cancel()
	}()

	db, err := database.NewClient(ctx, cfg.MongoURI, cfg.MongoDBTimeout)
	if err != nil {
		logger.For("main").Error("database connect failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := db.Close(closeCtx); err != nil {
			logger.For("main").Error("database close failed", "err", err)
		}
	}()

	if err := db.Ping(ctx); err != nil {
		logger.For("main").Error("database ping failed", "err", err)
		os.Exit(1)
	}
	if err := db.EnsureIndexes(ctx); err != nil {
		logger.For("main").Error("database indexes failed", "err", err)
		os.Exit(1)
	}
	logger.PrintAlways("database ready")

	if err := bot.Run(ctx, cfg, db); err != nil {
		logger.For("main").Error("bot shutdown", "err", err)
		os.Exit(1)
	}
}
