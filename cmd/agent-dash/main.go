// Command agent-dash is the agent-dash server: app UI, auth, OAuth, /mcp,
// and the site file server in one binary. Configuration comes from the
// environment; see docs/design.md#configuration.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mjquinlan2000/agent-dash/internal/config"
	"github.com/mjquinlan2000/agent-dash/internal/db"
	"github.com/mjquinlan2000/agent-dash/internal/server"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		// Plain text so multi-line config errors stay readable in pod logs.
		fmt.Fprintln(os.Stderr, "agent-dash:", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	deps := &server.Deps{Config: cfg, Logger: log, DB: pool}
	handler, err := server.New(deps, modules...)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.ListenAddr, "app_origin", cfg.AppOrigin.String(), "sites_domain", cfg.SitesDomain)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
