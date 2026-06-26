// Package reader is voodu-hep3's READ side. HEP_STORE selects how it serves
// the SIP data clowk-hep3 wrote:
//
//	ndjson (default) — tail/export the shared NDJSON volume (the webui
//	                   poller pulls /export?since=<cursor> into its SQLite)
//	pg               — REST query API over the shared Postgres (SELECT only;
//	                   clowk-hep3 owns the schema and the write path)
//
// The reader reads from exactly ONE backend (unlike the writer, which can
// dual-write). See ~/code/plans/hep3-stack.md (section 2026-06-26).
package reader

import (
	"fmt"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the reader's runtime configuration.
type Config struct {
	// Store selects the backend to read from: "ndjson" (default) or "pg".
	Store string `env:"HEP_STORE" envDefault:"ndjson"`
	// DataDir is the shared NDJSON volume (ndjson store).
	DataDir string `env:"HEP_DATA_DIR" envDefault:"/data"`
	// DatabaseURL is the shared Postgres connection string — required only
	// when Store is "pg".
	DatabaseURL string `env:"DATABASE_URL"`
	// APIAddr is the HTTP listen address.
	APIAddr string `env:"HEP3_API_ADDR" envDefault:"0.0.0.0:8080"`
}

// Load resolves configuration: optional .env, then environment overlay.
func Load() (Config, error) {
	_ = godotenv.Load()

	var c Config

	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}

	c.Store = strings.ToLower(strings.TrimSpace(c.Store))

	if c.Store == "" {
		c.Store = "ndjson"
	}

	if strings.Contains(c.Store, ",") {
		return Config{}, fmt.Errorf("HEP_STORE for the reader must be a single backend (ndjson or pg), got %q", c.Store)
	}

	if c.Store != "ndjson" && c.Store != "pg" {
		return Config{}, fmt.Errorf("invalid HEP_STORE %q (want ndjson or pg)", c.Store)
	}

	if c.Store == "pg" && strings.TrimSpace(c.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required when HEP_STORE=pg")
	}

	return c, nil
}
