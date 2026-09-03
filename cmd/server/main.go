package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ollama-webui/internal/agent"
	"ollama-webui/internal/config"
	"ollama-webui/internal/httpapi"
	"ollama-webui/internal/ollama"
	"ollama-webui/internal/store"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	database, err := store.Open(ctx, cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	workspace, err := filepath.Abs(cfg.AgentWorkspace)
	if err != nil {
		logger.Error("resolve agent workspace", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		logger.Error("create agent workspace", "error", err)
		os.Exit(1)
	}

	ollamaClient := ollama.NewClient(cfg.OllamaBaseURL)
	runner := agent.Runner{
		Chat: ollamaClient, Steps: database,
		Shell:             agent.ShellExecutor{Workspace: workspace, Timeout: cfg.ShellTimeout, MaxOutput: cfg.ShellMaxOutput},
		Stager:            agent.InputStager{Workspace: workspace, MaxInlineBytes: cfg.InlineInputMax},
		MaxTurns:          cfg.AgentMaxTurns,
		ToolFeedbackLimit: cfg.ToolFeedbackMax,
	}
	handler := httpapi.New(database, ollamaClient, runner, httpapi.RuntimeSettings{
		Workspace: workspace, MaxTurns: cfg.AgentMaxTurns, ShellTimeout: cfg.ShellTimeout,
		ShellMaxOutput: cfg.ShellMaxOutput, InlineInputMax: cfg.InlineInputMax, ToolFeedbackLimit: cfg.ToolFeedbackMax,
	}, cfg.WebDistDir, logger)
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		logger.Info("server listening", "address", cfg.ListenAddr, "ollama", cfg.OllamaBaseURL)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("serve", "error", err)
			stop()
		}
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "error", err)
	}
}
