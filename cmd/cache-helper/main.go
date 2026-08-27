package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/config"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           httpapi.New(cfg, logger),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("cache helper listening", "address", cfg.ListenAddress, "store_root", cfg.StoreRoot)
		serverErrors <- server.ListenAndServe()
	}()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-signals:
		logger.Info("shutdown requested", "signal", sig.String())
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
		return
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownPeriod)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
