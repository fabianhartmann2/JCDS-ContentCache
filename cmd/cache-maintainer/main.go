package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fabianhartmann2/JCDS-ContentCache/internal/maintenance"
)

func main() {
	if err := run(); err != nil {
		slog.Error("cache maintainer stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := maintenance.LoadConfig()
	if err != nil {
		return err
	}
	index, err := maintenance.LoadIndex(cfg.IndexPath)
	if err != nil {
		return err
	}
	cleaner := &maintenance.Cleaner{
		StoreRoot:      cfg.StoreRoot,
		AuditPath:      cfg.AuditPath,
		Retention:      cfg.Retention,
		TriggerPercent: cfg.TriggerPercent,
		TargetPercent:  cfg.TargetPercent,
		Index:          index,
	}
	packet, err := net.ListenPacket("udp", cfg.UDPListen)
	if err != nil {
		return err
	}
	defer packet.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"live"}`))
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	httpServer := &http.Server{Addr: cfg.HTTPListen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	var wait sync.WaitGroup
	wait.Add(3)
	go func() {
		defer wait.Done()
		buffer := make([]byte, 4096)
		for {
			_ = packet.SetReadDeadline(time.Now().Add(time.Second))
			count, _, err := packet.ReadFrom(buffer)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					select {
					case <-ctx.Done():
						return
					default:
						continue
					}
				}
				return
			}
			if filename, ok := maintenance.ParseAccessEvent(buffer[:count]); ok {
				_ = index.Record(filename, time.Now())
			}
		}
	}()
	go func() {
		defer wait.Done()
		flush := time.NewTicker(cfg.FlushInterval)
		cleanup := time.NewTicker(cfg.CleanupInterval)
		defer flush.Stop()
		defer cleanup.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-flush.C:
				if err := index.Flush(); err != nil {
					slog.Error("access index flush failed", "error", err)
				}
			case <-cleanup.C:
				result, err := cleaner.Run()
				if err != nil {
					slog.Error("cache cleanup failed", "error", err)
					continue
				}
				if result.Triggered {
					if result.TargetReached {
						slog.Info("cache cleanup target reached", "removed_files", result.RemovedFiles, "removed_bytes", result.RemovedBytes)
					} else {
						slog.Warn("cache cleanup target not reached", "removed_files", result.RemovedFiles, "removed_bytes", result.RemovedBytes)
					}
				}
			}
		}
	}()
	go func() {
		defer wait.Done()
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health server failed", "error", err)
			stop()
		}
	}()

	slog.Info("cache maintainer ready", "retention", cfg.Retention, "trigger_free_percent", cfg.TriggerPercent, "target_free_percent", cfg.TargetPercent)
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	_ = packet.Close()
	wait.Wait()
	return index.Flush()
}
