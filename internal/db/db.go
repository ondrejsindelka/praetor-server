// Package db manages the Postgres connection pool and schema migrations.
package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var Migrations embed.FS

// Connect opens a pgxpool with sane defaults and pings the server.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.MaxConns = 25
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

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

// Migrate runs all pending goose Up migrations from the provided embedded FS.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fs embed.FS) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()

	goose.SetBaseFS(fs)
	goose.SetLogger(&slogGooseLogger{})
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("db: migrate set dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, "migrations"); err != nil {
		return fmt.Errorf("db: migrate up: %w", err)
	}
	return nil
}

// Close shuts down the connection pool.
func Close(pool *pgxpool.Pool) {
	pool.Close()
}

// slogGooseLogger forwards goose log output to slog.
type slogGooseLogger struct{}

func (l *slogGooseLogger) Fatalf(format string, v ...any) {
	slog.Error(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
}

func (l *slogGooseLogger) Printf(format string, v ...any) {
	slog.Info(strings.TrimRight(fmt.Sprintf(format, v...), "\n"))
}
