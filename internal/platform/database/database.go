// Package database manages the application's PostgreSQL connection pool.
package database

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const connectionTimeout = 5 * time.Second

// Open verifies connectivity before returning. The caller owns and must close
// the pool after HTTP requests have drained.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid DATABASE_URL connection configuration")
	}
	cfg.ConnConfig.ConnectTimeout = connectionTimeout
	cfg.MaxConns = 10
	cfg.MinConns = 0
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("could not create PostgreSQL connection pool")
	}
	pingCtx, cancel := context.WithTimeout(ctx, connectionTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		// Do not propagate driver errors: they can include connection credentials.
		return nil, errors.New("PostgreSQL startup check failed; check DATABASE_URL and database availability")
	}
	return pool, nil
}
