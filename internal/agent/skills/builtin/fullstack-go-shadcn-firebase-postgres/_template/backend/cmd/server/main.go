// Command server runs the __APP__ API.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"__MODULE__/backend/internal/api"
	"__MODULE__/backend/internal/auth"
	"__MODULE__/backend/internal/config"
	"__MODULE__/backend/internal/db"
	"__MODULE__/backend/internal/db/sqlc"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	verifier, err := auth.NewVerifier(ctx, cfg.FirebaseProjectID, cfg.AuthDevBypass)
	if err != nil {
		return err
	}
	if cfg.AuthDevBypass {
		log.Warn("AUTH_DEV_BYPASS is on: the token \"dev\" signs in as a test user; never use this in production")
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(sqlc.New(pool), verifier, cfg.CORSOrigin, log).Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Addr)
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}
