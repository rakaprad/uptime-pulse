// Command uptime-pulse monitors HTTP endpoints, alerts on state changes and
// exposes Prometheus metrics and a status page.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rakaprad/uptime-pulse/internal/checker"
	"github.com/rakaprad/uptime-pulse/internal/config"
	"github.com/rakaprad/uptime-pulse/internal/metrics"
	"github.com/rakaprad/uptime-pulse/internal/notify"
	"github.com/rakaprad/uptime-pulse/internal/server"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}
	if err := run(*configPath); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	// SIGTERM is what Kubernetes and Docker send on stop; SIGINT is Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	m := metrics.New()
	notifier := buildNotifier(cfg.Notifiers, log)
	mon := checker.NewMonitor(cfg, checker.New("uptime-pulse/"+version), notifier, m, log)

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           server.Handler(mon, m.Handler(), version, log),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", cfg.Listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	monitorDone := make(chan struct{})
	go func() {
		mon.Run(ctx)
		close(monitorDone)
	}()
	log.Info("uptime-pulse started", "version", version, "targets", len(cfg.Targets))

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-serverErr:
		stop() // stop the monitor too
		<-monitorDone
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("http shutdown", "error", err)
	}
	<-monitorDone
	log.Info("bye")
	return nil
}

func buildNotifier(cfg config.Notifiers, log *slog.Logger) notify.Notifier {
	var ns notify.Multi
	if cfg.Telegram.Enabled() {
		ns = append(ns, &notify.Telegram{
			BotToken: cfg.Telegram.BotToken,
			ChatID:   cfg.Telegram.ChatID,
			ThreadID: cfg.Telegram.MessageThreadID,
		})
	}
	if cfg.Slack.Enabled() {
		ns = append(ns, &notify.Slack{WebhookURL: cfg.Slack.WebhookURL})
	}

	names := make([]string, 0, len(ns))
	for _, n := range ns {
		names = append(names, n.Name())
	}
	if len(ns) == 0 {
		log.Warn("no notifiers configured; state changes will only be logged")
		return nil
	}
	log.Info("notifiers enabled", "notifiers", strings.Join(names, ","))
	return ns
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.EqualFold(format, "text") {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}
