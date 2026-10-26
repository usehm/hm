// Package config reads the server's settings from the environment.
package config

import (
	"errors"
	"os"
)

// Config is the server's settings.
type Config struct {
	Addr              string // ADDR, default :8080
	DatabaseURL       string // DATABASE_URL, required
	FirebaseProjectID string // FIREBASE_PROJECT_ID; required unless AuthDevBypass
	AuthDevBypass     bool   // AUTH_DEV_BYPASS=1 accepts the token "dev" (local development only)
	CORSOrigin        string // CORS_ORIGIN, default http://localhost:5173
}

// Load reads Config from the environment.
func Load() (Config, error) {
	c := Config{
		Addr:              env("ADDR", ":8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		FirebaseProjectID: os.Getenv("FIREBASE_PROJECT_ID"),
		AuthDevBypass:     os.Getenv("AUTH_DEV_BYPASS") == "1",
		CORSOrigin:        env("CORS_ORIGIN", "http://localhost:5173"),
	}
	if c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is not set")
	}
	if c.FirebaseProjectID == "" && !c.AuthDevBypass {
		return c, errors.New("FIREBASE_PROJECT_ID is not set (or set AUTH_DEV_BYPASS=1 for local development)")
	}
	return c, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
