package migrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const TableName = "relay_schema_migrations"

type Runner struct {
	db *sql.DB
}

type AppliedMigration struct {
	Version   int64     `json:"version"`
	Name      string    `json:"name"`
	AppliedAt time.Time `json:"applied_at"`
}

type Status struct {
	Version   int64      `json:"version"`
	Name      string     `json:"name"`
	Applied   bool       `json:"applied"`
	AppliedAt *time.Time `json:"applied_at,omitempty"`
}

func Open(ctx context.Context, dsn string) (*Runner, error) {
	if dsn == "" {
		return nil, errors.New("database DSN is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Runner{db: db}, nil
}

func NewRunner(db *sql.DB) *Runner {
	return &Runner{db: db}
}

func (r *Runner) Close() error {
	if r == nil || r.db == nil {
		return nil
	}
	return r.db.Close()
}

func (r *Runner) EnsureTable(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS relay_schema_migrations (
    version BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`)
	return err
}

func (r *Runner) Applied(ctx context.Context) (map[int64]AppliedMigration, error) {
	if err := r.EnsureTable(ctx); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT version, name, applied_at FROM relay_schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	applied := map[int64]AppliedMigration{}
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.AppliedAt); err != nil {
			return nil, err
		}
		applied[migration.Version] = migration
	}
	return applied, rows.Err()
}

func (r *Runner) Status(ctx context.Context, migrations []Migration) ([]Status, error) {
	applied, err := r.Applied(ctx)
	if err != nil {
		return nil, err
	}
	statuses := make([]Status, 0, len(migrations))
	for _, migration := range migrations {
		status := Status{
			Version: migration.Version,
			Name:    migration.Name,
		}
		if appliedMigration, ok := applied[migration.Version]; ok {
			status.Applied = true
			status.AppliedAt = &appliedMigration.AppliedAt
		}
		statuses = append(statuses, status)
	}
	return statuses, nil
}

func (r *Runner) Up(ctx context.Context, migrations []Migration) ([]AppliedMigration, error) {
	if err := r.EnsureTable(ctx); err != nil {
		return nil, err
	}
	applied, err := r.Applied(ctx)
	if err != nil {
		return nil, err
	}

	var completed []AppliedMigration
	for _, migration := range migrations {
		if _, ok := applied[migration.Version]; ok {
			continue
		}
		appliedMigration, err := r.applyUp(ctx, migration)
		if err != nil {
			return completed, err
		}
		completed = append(completed, appliedMigration)
	}
	return completed, nil
}

func (r *Runner) Down(ctx context.Context, migrations []Migration, steps int) ([]AppliedMigration, error) {
	if steps <= 0 {
		return nil, errors.New("steps must be positive")
	}
	if err := r.EnsureTable(ctx); err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(ctx, `SELECT version, name, applied_at FROM relay_schema_migrations ORDER BY version DESC LIMIT $1`, steps)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rollbackTargets []AppliedMigration
	for rows.Next() {
		var migration AppliedMigration
		if err := rows.Scan(&migration.Version, &migration.Name, &migration.AppliedAt); err != nil {
			return nil, err
		}
		rollbackTargets = append(rollbackTargets, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var completed []AppliedMigration
	for _, target := range rollbackTargets {
		migration, ok := FindByVersion(migrations, target.Version)
		if !ok {
			return completed, fmt.Errorf("applied migration %d has no local down SQL", target.Version)
		}
		if err := r.applyDown(ctx, migration); err != nil {
			return completed, err
		}
		completed = append(completed, target)
	}
	return completed, nil
}

func (r *Runner) applyUp(ctx context.Context, migration Migration) (AppliedMigration, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return AppliedMigration{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.UpSQL); err != nil {
		return AppliedMigration{}, fmt.Errorf("apply migration %d_%s: %w", migration.Version, migration.Name, err)
	}
	var appliedAt time.Time
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO relay_schema_migrations(version, name) VALUES($1, $2) RETURNING applied_at`,
		migration.Version,
		migration.Name,
	).Scan(&appliedAt); err != nil {
		return AppliedMigration{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppliedMigration{}, err
	}
	return AppliedMigration{Version: migration.Version, Name: migration.Name, AppliedAt: appliedAt}, nil
}

func (r *Runner) applyDown(ctx context.Context, migration Migration) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, migration.DownSQL); err != nil {
		return fmt.Errorf("rollback migration %d_%s: %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM relay_schema_migrations WHERE version = $1`, migration.Version); err != nil {
		return err
	}
	return tx.Commit()
}
