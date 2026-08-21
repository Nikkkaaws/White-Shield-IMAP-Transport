package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Nikkkaaws/wsit/internal/config"
	"github.com/Nikkkaaws/wsit/internal/engine"
)

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml")
	mode := flag.String("mode", "", "override mode: client, server, probe")
	checkConfig := flag.Bool("check-config", false, "validate config and exit")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("config", "err", err)
		os.Exit(2)
	}
	if *mode != "" {
		cfg.Mode = strings.ToLower(*mode)
		if err := cfg.Validate(); err != nil {
			slog.Error("config", "err", err)
			os.Exit(2)
		}
	}
	if *checkConfig {
		fmt.Println("WSIT config: ok")
		return
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
		log.Error("engine", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch cfg.Mode {
	case "probe":
		err = eng.Probe(ctx)
	default:
		err = eng.Run(ctx)
	}
	if err != nil && ctx.Err() == nil {
		log.Error("exit", "err", err)
		os.Exit(1)
	}
}
