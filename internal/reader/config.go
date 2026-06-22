// Package reader is voodu-hep3's READ side: the REST API that queries the
// SIP messages clowk-hep3 wrote to the shared Postgres. It never writes —
// clowk-hep3 owns the schema and the write path; the reader connects to
// the same DATABASE_URL and only SELECTs.
package reader

import (
	"fmt"
	"strings"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the reader's runtime configuration.
type Config struct {
	// DatabaseURL is the shared Postgres connection string (required) —
	// the same one clowk-hep3 writes to.
	DatabaseURL string `env:"DATABASE_URL,required"`
	// APIAddr is the REST API listen address.
	APIAddr string `env:"HEP3_API_ADDR" envDefault:"0.0.0.0:8080"`
}

// Load resolves configuration: optional .env, then environment overlay.
func Load() (Config, error) {
	_ = godotenv.Load()

	var c Config

	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}

	if strings.TrimSpace(c.DatabaseURL) == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}

	return c, nil
}
