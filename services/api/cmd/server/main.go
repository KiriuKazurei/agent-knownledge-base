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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/config"
	"github.com/KiriuKazurei/agent-knownledge-base/services/api/internal/diagnostics"
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
	logWriter := &diagnostics.Writer{Path: filepath.Join(cfg.DataRoot, "logs", "api.log")}
	logger := slog.New(slog.NewJSONHandler(logWriter, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
	if err := api.ResumeArchitectureTasks(rootCtx); err != nil {
		logger.Error("architecture task recovery failed", "error", err)
	}
	if err := api.ResumeRetrievalIndexes(rootCtx); err != nil {
		logger.Error("retrieval index recovery failed", "error", err)
	}
	api.StartArchitectureScheduler(rootCtx)
	api.StartSourceWatchers(rootCtx)
	server := &http.Server{Addr: cfg.Host + ":" + cfg.Port, Handler: api.Router(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	stop := make(chan os.Signal, 1)
	var updateShutdown atomic.Bool
	api.SetRuntimeShutdown(func() {
		updateShutdown.Store(true)
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(ctx)
	})
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
		ctx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancelShutdown()
		_ = server.Shutdown(ctx)
	}()
	workerState := "unavailable"
	if workerClient != nil {
		workerState = workerClient.State()
	}
	logger.Info("api listening", "address", server.Addr, "dataRoot", cfg.DataRoot, "worker", workerState)
	fmt.Printf("KAH_API_READY http://%s%s\n", server.Addr, "/api/v1")
	err = server.ListenAndServe()
	if workerClient != nil {
		if updateShutdown.Load() {
			if closeErr := workerClient.CloseGracefully(15 * time.Second); closeErr != nil {
				logger.Error("update shutdown failed", "error", closeErr)
				// Keep the process alive while the worker exits. The desktop's
				// 20s timeout aborts replacement; never falsely report a clean exit.
				_ = workerClient.CloseGracefully(24 * time.Hour)
			}
		} else {
			_ = workerClient.Close()
		}
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
