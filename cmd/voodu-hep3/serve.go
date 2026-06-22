package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/thadeu/voodu-hep3/internal/reader"
)

const serveHelp = `voodu-hep3 serve

Run the read-only REST API over the shared Postgres that clowk-hep3 writes
to. This is the container the voodu-hep3 plugin deploys (build-mode) and
the controller reverse-proxies to. Config via env:

  DATABASE_URL     shared Postgres connection string (required)
  HEP3_API_ADDR    listen address (default 0.0.0.0:8080)

The API is versioned by media type, e.g.
  Accept: application/vnd.clowk.hep+json;version=1
`

// cmdServe runs the REST API server. It blocks until SIGINT/SIGTERM.
func cmdServe() error {
	args := os.Args[2:]
	if hasHelpFlag(args) {
		os.Stdout.WriteString(serveHelp)

		return nil
	}

	cfg, err := reader.Load()
	if err != nil {
		return err
	}

	rd, err := reader.NewReader(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = rd.Close() }()

	logger := log.New(os.Stderr, "", log.LstdFlags|log.LUTC)

	srv := &http.Server{
		Addr:              cfg.APIAddr,
		Handler:           reader.NewAPI(rd).Handler(),
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

	logger.Printf("voodu-hep3: REST API listening on %s (reading shared postgres)", cfg.APIAddr)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	logger.Printf("voodu-hep3: API shutting down")

	return nil
}
