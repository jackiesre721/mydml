package config

import (
	"context"
	"database/sql"
	"log/slog"
	"time"
)

type LogDB struct {
	*sql.DB
	cfg *Config
}

func NewLogDB(db *sql.DB, cfg *Config) *LogDB {
	return &LogDB{DB: db, cfg: cfg}
}

func (d *LogDB) Config() *Config {
	return d.cfg
}

func (d *LogDB) ExecSQL(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	annotated := d.cfg.Annotate(query)
	start := time.Now()
	result, err := d.DB.ExecContext(ctx, annotated, args...)
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("SQL exec failed",
			slog.String("sql", truncate(annotated)),
			slog.Duration("duration", elapsed),
			slog.String("error", err.Error()),
		)
		return nil, err
	}
	affected, _ := result.RowsAffected()
	slog.Info("SQL exec",
		slog.String("sql", truncate(annotated)),
		slog.Int64("affected", affected),
		slog.Duration("duration", elapsed),
	)
	return result, nil
}

func (d *LogDB) QuerySQL(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	annotated := d.cfg.Annotate(query)
	start := time.Now()
	rows, err := d.DB.QueryContext(ctx, annotated, args...)
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("SQL query failed",
			slog.String("sql", truncate(annotated)),
			slog.Duration("duration", elapsed),
			slog.String("error", err.Error()),
		)
		return nil, err
	}
	slog.Info("SQL query",
		slog.String("sql", truncate(annotated)),
		slog.Duration("duration", elapsed),
	)
	return rows, nil
}

func (d *LogDB) QueryRowSQL(ctx context.Context, query string, args ...interface{}) *sql.Row {
	annotated := d.cfg.Annotate(query)
	start := time.Now()
	row := d.DB.QueryRowContext(ctx, annotated, args...)
	elapsed := time.Since(start)
	slog.Info("SQL queryRow",
		slog.String("sql", truncate(annotated)),
		slog.Duration("duration", elapsed),
	)
	return row
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500] + "..."
	}
	return s
}
