package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/auth"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/config"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/download"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/httpapi"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/jamf"
	"github.com/fabianhartmann2/JCDS-ContentCache/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("cache helper stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	packageStore, err := store.New(cfg.StoreRoot, cfg.TempRoot)
	if err != nil {
		return err
	}

	serviceClient := &http.Client{
		Transport: newTransport(),
		Timeout:   30 * time.Second,
	}
	tokenProvider := auth.NewProvider(
		serviceClient,
		cfg.TokenURL,
		cfg.ClientID,
		cfg.ClientSecret,
		cfg.TokenExpirySkew,
	)
	resolver := jamf.NewClient(
		serviceClient,
		tokenProvider,
		cfg.ResolverURLTemplate,
		cfg.DownloadURLField,
	)
	downloadClient := download.NewClient(
		&http.Client{Transport: newTransport()},
		download.NewPolicy(cfg.AllowedDownloadHosts, cfg.AllowHTTP),
		cfg.MaxPackageBytes,
	)
	api := httpapi.New(
		logger,
		packageStore,
		resolver,
		downloadClient,
		cfg.FillTimeout,
		cfg.MaxPackageBytes,
	)

	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    32 * 1024,
	}

	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	serverError := make(chan error, 1)
	go func() {
		logger.Info("cache helper listening", "address", cfg.ListenAddress)
		serverError <- server.ListenAndServe()
	}()

	select {
	case err := <-serverError:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-shutdownSignal.Done():
		logger.Info("cache helper shutting down")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		return err
	}
	return nil
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}
