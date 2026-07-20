// Command server runs the go-suggest-neo HTTP service.
//
// Boot sequence:
//  1. Load config.
//  2. Build the server Instance (opens current.version's corpus, or creates
//     an empty corpus on a brand-new data directory).
//  3. Serve. The corpus can be replaced at runtime via /upload or /restore
//     and reloaded in-process — no external supervisor required.
//  4. SIGINT/SIGTERM → graceful shutdown (flush snapshots, close corpus).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"go-suggest-neo/internal/config"
	"go-suggest-neo/internal/server"

	"gopkg.in/natefinch/lumberjack.v2"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "fatal config error:", err)
		os.Exit(2)
	}
	configureLogger(cfg)

	projects, err := server.NewProjectManager(cfg)
	if err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	inst := projects.Default()
	app := server.NewWithProjectManager(projects)
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	slog.Info("listening", "addr", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := app.Listen(addr); err != nil && !errors.Is(err, os.ErrClosed) {
			errCh <- err
		}
	}()

	select {
	case <-quit:
		slog.Info("shutdown signal received")
	case err := <-errCh:
		slog.Error("server error", "err", err)
	}

	shutCtx, shutCancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.RestartFlushTimeoutSec)*time.Second)
	defer shutCancel()

	if err := app.ShutdownWithContext(shutCtx); err != nil {
		slog.Warn("http shutdown error", "err", err)
	}
	inst.Stop(shutCtx)
	slog.Info("bye")
}

func configureLogger(cfg *config.Config) {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	var out io.Writer = os.Stdout
	if cfg.LogFile != "" {
		if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "log dir create failed:", err)
		} else {
			rot := &lumberjack.Logger{
				Filename:   cfg.LogFile,
				MaxSize:    cfg.LogMaxSizeMB,
				MaxBackups: cfg.LogMaxBackups,
				MaxAge:     cfg.LogMaxAgeDays,
				Compress:   cfg.LogCompress,
				LocalTime:  true,
			}
			out = io.MultiWriter(os.Stdout, rot)
		}
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(out,
		&slog.HandlerOptions{Level: level})))
}
