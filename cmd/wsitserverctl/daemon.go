package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/engine"
)

func runServerDaemon(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	cfg.Mode = "server"
	if err := cfg.Validate(); err != nil {
		return err
	}
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	eng, err := engine.New(cfg, log)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	defer removeServerStatus()
	_ = writeServerStatus(transportStatus{Phase: "starting", Stage: "Подключение почтовых линий", Target: cfg.Target})
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			stats := eng.Stats()
			phase, stage := "starting", "Подключение почтовых линий"
			if stats.LiveLanes > 0 {
				phase, stage = "running", "Работает"
			}
			_ = writeServerStatus(transportStatus{
				Phase: phase, Stage: stage, Target: cfg.Target,
				LiveLanes: stats.LiveLanes, TXBytes: stats.TXBytes, RXBytes: stats.RXBytes,
				ActiveStreams: stats.ActiveStreams, PendingBytes: stats.PendingBytes,
				Appends: stats.Appends,
			})
		}
	}
}
