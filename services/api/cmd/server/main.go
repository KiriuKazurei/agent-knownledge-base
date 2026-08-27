package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/httpapi"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/storage"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(cfg.DataRoot, "logs"), 0o750); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(cfg.DataRoot, "logs", "api.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	logger := slog.New(slog.NewJSONHandler(logFile, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	store, err := storage.Open(cfg.DataRoot)
	if err != nil {
		return err
	}
	defer store.Close()
	rootCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workerClient *worker.Client
	if cfg.WorkerCmd != "" {
		workingDirectory := os.Getenv("KAH_WORKER_CWD")
		if workingDirectory == "" {
			workingDirectory = "."
		}
		workerClient, err = worker.Start(rootCtx, cfg.WorkerCmd, cfg.WorkerArgs, workingDirectory, cfg.DataRoot)
		if err != nil {
			logger.Warn("worker unavailable; text fallback remains active", "error", err)
		}
	}
	api := httpapi.New(cfg, store, workerClient, logger)
	if err := api.ResumeQueuedJobs(rootCtx); err != nil {
		logger.Error("queued job recovery failed", "error", err)
	}
	server := &http.Server{Addr: cfg.Host + ":" + cfg.Port, Handler: api.Router(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(ctx)
	}()
	logger.Info("api listening", "address", server.Addr, "dataRoot", cfg.DataRoot, "worker", workerClient.State())
	fmt.Printf("KAH_API_READY http://%s%s\n", server.Addr, "/api/v1")
	err = server.ListenAndServe()
	if workerClient != nil {
		_ = workerClient.Close()
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
