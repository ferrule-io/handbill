// Command handbill is the handbill server: app UI, auth, OAuth, /mcp,
// and the site file server in one binary. Configuration comes from the
// environment; see docs/design.md#configuration.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/sync/errgroup"

	"github.com/ferrule-io/handbill/internal/config"
	"github.com/ferrule-io/handbill/internal/db"
	"github.com/ferrule-io/handbill/internal/httpserver"
	"github.com/ferrule-io/handbill/internal/maintenance"
	"github.com/ferrule-io/handbill/internal/metrics"
	"github.com/ferrule-io/handbill/internal/server"
	"github.com/ferrule-io/handbill/internal/storage"
	"github.com/ferrule-io/handbill/internal/version"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(version.String())
		return
	}
	if len(os.Args) > 1 {
		fmt.Fprintf(os.Stderr, "usage: %s [--version]\nConfiguration comes from the environment; see docs/design.md#configuration.\n", os.Args[0])
		os.Exit(2)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		// Plain text so multi-line config errors stay readable in pod logs.
		fmt.Fprintln(os.Stderr, "handbill:", err)
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
	log.Info("starting", "version", version.Version, "commit", version.Commit)

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	store, err := storage.Open(ctx, cfg.Storage)
	if err != nil {
		return err
	}
	deps := &server.Deps{Config: cfg, Logger: log, DB: pool, Storage: store}
	handler, err := server.New(deps, modules...)
	if err != nil {
		return err
	}

	ops := cfg.Operations
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	var metricsLn net.Listener
	if ops.MetricsAddr != "" {
		if metricsLn, err = net.Listen("tcp", ops.MetricsAddr); err != nil {
			ln.Close()
			return err
		}
		metrics.Registry.MustRegister(metrics.NewPoolCollector(pool))
	}
	log.Info("listening", "addr", ln.Addr().String(), "metrics_addr", ops.MetricsAddr,
		"app_origin", cfg.AppOrigin.String(), "sites_domain", cfg.SitesDomain, "version", version.Version)

	// Maintenance stops with the servers; it is not drained.
	var bg sync.WaitGroup
	maintCtx, stopMaint := context.WithCancel(ctx)
	bg.Go(func() { maintenance.New(deps).Run(maintCtx) })
	defer func() { stopMaint(); bg.Wait() }()

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return httpserver.Serve(gctx, httpserver.New(cfg.ListenAddr, handler, log), ln, ops.ShutdownTimeout, log)
	})
	if metricsLn != nil {
		mux := http.NewServeMux()
		mux.Handle("GET /metrics", metrics.Handler())
		g.Go(func() error {
			return httpserver.Serve(gctx, httpserver.New(ops.MetricsAddr, mux, log), metricsLn, ops.ShutdownTimeout, log)
		})
	}
	<-gctx.Done()
	if ctx.Err() != nil {
		log.Info("shutting down", "drain", ops.ShutdownTimeout.String())
	}
	return g.Wait()
}
