// Package db provides a PostgreSQL connection pool and migration runner for
// Sentinel NVR.
package db

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB wraps a pgxpool.Pool and exposes helper methods used throughout Sentinel.
type DB struct {
	pool *pgxpool.Pool
}

// New creates a connection pool, verifies connectivity, and runs all pending
// schema migrations. maxConns is capped at 100; pass 0 to use the default 10.
func New(ctx context.Context, url string, maxConns int32) (*DB, error) {
	if maxConns <= 0 {
		maxConns = 10
	}
	if maxConns > 100 {
		maxConns = 100
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("db: parse config: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.MinConns = 2
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}

	slog.Info("database connected", "max_conns", maxConns)

	d := &DB{pool: pool}
	if err := d.runMigrations(url); err != nil {
		pool.Close()
		return nil, err
	}
	return d, nil
}

// runMigrations applies all pending SQL migrations from the embedded FS.
func (d *DB) runMigrations(url string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("db: migrations source: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, url)
	if err != nil {
		return fmt.Errorf("db: migrate init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("db: migrate up: %w", err)
	}

	ver, dirty, _ := m.Version()
	slog.Info("migrations applied", "version", ver, "dirty", dirty)
	return nil
}

// Pool returns the underlying pgxpool for direct query use.
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// Close shuts down the connection pool.
func (d *DB) Close() {
	d.pool.Close()
	slog.Info("database pool closed")
}

// Ping verifies at least one live connection exists.
func (d *DB) Ping(ctx context.Context) error {
	if err := d.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db: ping: %w", err)
	}
	return nil
}

// Exec is a convenience wrapper around pgxpool.Pool.Exec.
func (d *DB) Exec(ctx context.Context, sql string, args ...any) error {
	_, err := d.pool.Exec(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("db exec: %w", err)
	}
	return nil
}

// QueryRow is a convenience wrapper around pgxpool.Pool.QueryRow.
func (d *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return d.pool.QueryRow(ctx, sql, args...)
}

// Query is a convenience wrapper around pgxpool.Pool.Query.
func (d *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := d.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("db query: %w", err)
	}
	return rows, nil
}

// WithTx executes fn inside a serializable transaction. If fn returns an
// error the transaction is rolled back; otherwise it is committed.
func (d *DB) WithTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := d.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	defer func() {
		// Best-effort rollback if we haven't committed.
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit tx: %w", err)
	}
	return nil
}
