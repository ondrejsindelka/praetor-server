// Package db manages the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// New creates and validates a pgxpool connection pool.
func New(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	return pool, nil
}

// Migrate runs goose migrations using the embedded SQL files.
// direction must be "up", "down", or "status".
func Migrate(ctx context.Context, pool *pgxpool.Pool, direction string) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: set dialect: %w", err)
	}

	switch direction {
	case "up":
		if err := goose.UpContext(ctx, db, "migrations"); err != nil {
			return fmt.Errorf("db: migrate up: %w", err)
		}
	case "down":
		if err := goose.DownContext(ctx, db, "migrations"); err != nil {
			return fmt.Errorf("db: migrate down: %w", err)
		}
	case "status":
		if err := goose.StatusContext(ctx, db, "migrations"); err != nil {
			return fmt.Errorf("db: migrate status: %w", err)
		}
	default:
		return fmt.Errorf("db: unknown direction %q (want up|down|status)", direction)
	}

	return nil
}
