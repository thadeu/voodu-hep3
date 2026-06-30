package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thadeu/voodu-hep3/internal/exporter"
	"github.com/thadeu/voodu-hep3/internal/reader"
)

const serveHelp = `voodu-hep3 serve

Serve the SIP data clowk-hep3 captured. This is the container the
voodu-hep3 plugin deploys and the controller reverse-proxies to. HEP_STORE
selects what it serves:

  ndjson (default)  GET /export?since=<cursor>  — NDJSON tail of the shared
                    volume; the webui poller pulls it into its SQLite.
  pg                REST query API over the shared Postgres (/calls,
                    /calls/{id}, /stats), JSON responses.

Config via env:

  HEP_STORE       ndjson | pg              (default ndjson)
  HEP_DATA_DIR    shared NDJSON volume     (default /data; ndjson store)
  DATABASE_URL    Postgres connection      (required when HEP_STORE=pg)
  HEP3_API_ADDR   listen address           (default 0.0.0.0:8080)
`

// cmdServe runs the HTTP server for the configured backend. It blocks until
// SIGINT/SIGTERM.
func cmdServe() error {
	args := os.Args[2:]
	if hasHelpFlag(args) {
		fmt.Print(serveHelp)

		return nil
	}

	cfg, err := reader.Load()
	if err != nil {
		return err
	}

	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)

	var (
		handler http.Handler
		cleanup func()
		mode    string
	)

	switch cfg.Store {
	case "ndjson":
		handler = exporter.New(cfg.DataDir).Handler()
		mode = "ndjson tail of " + cfg.DataDir
	case "pg":
		rd, rerr := reader.NewReader(cfg.DatabaseURL)
		if rerr != nil {
			return rerr
		}

		cleanup = func() { _ = rd.Close() }
		handler = reader.NewAPI(rd).Handler()
		mode = "postgres query API"
	default:
		return fmt.Errorf("unsupported HEP_STORE %q", cfg.Store)
	}

	if cleanup != nil {
		defer cleanup()
	}

	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	}()

	logger.Printf("voodu-hep3: listening on %s (%s)", cfg.APIAddr, mode)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	logger.Printf("voodu-hep3: shutting down")

	return nil
}

// hasHelpFlag reports whether -h / --help appears in args.
func hasHelpFlag(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}

	return false
}
