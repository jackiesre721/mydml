package planner

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math/big"
	"strings"

	"github.com/jackiesre721/mydml/internal/config"
)

type Plan struct {
	MinID       *big.Int
	MaxID       *big.Int
	ChunkStep   int64
	TotalChunks int64
	ChunkColumn string
}

var numericTypes = map[string]bool{
	"tinyint": true, "smallint": true, "mediumint": true,
	"int": true, "bigint": true,
}

// New detects the PK column, validates it, and builds an execution plan.
func New(db *config.LogDB, cfg *config.Config) (*Plan, error) {
	ctx := context.Background()

	// 1. Detect PK column name(s)
	pkCols, err := detectPKColumns(ctx, db, cfg)
	if err != nil {
		return nil, err
	}
	if len(pkCols) == 0 {
		return nil, fmt.Errorf("table %s has no primary key (single numeric PK required)", cfg.Table)
	}
	if len(pkCols) > 1 {
		return nil, fmt.Errorf("table %s has composite primary key (%s): not supported, only single-column numeric PK",
			cfg.Table, strings.Join(pkCols, ", "))
	}
	chunkCol := pkCols[0]

	// 2. Validate PK is numeric
	dataType, err := getColumnDataType(ctx, db, cfg, chunkCol)
	if err != nil {
		return nil, err
	}
	if !numericTypes[strings.ToLower(dataType)] {
		return nil, fmt.Errorf("PK column %s is %s (non-numeric): only integer PK supported", chunkCol, dataType)
	}

	// 3. Query PK range via ORDER BY + LIMIT (faster than MIN/MAX on large tables)
	//    Read as string to handle BIGINT UNSIGNED (> int64 max) and negative values.
	var minStr, maxStr sql.NullString
	minQuery := fmt.Sprintf("SELECT CAST(`%s` AS CHAR) FROM `%s` ORDER BY `%s` ASC LIMIT 1", chunkCol, cfg.Table, chunkCol)
	if err := db.QueryRowSQL(ctx, minQuery).Scan(&minStr); err != nil {
		return nil, fmt.Errorf("query PK min: %w", err)
	}
	maxQuery := fmt.Sprintf("SELECT CAST(`%s` AS CHAR) FROM `%s` ORDER BY `%s` DESC LIMIT 1", chunkCol, cfg.Table, chunkCol)
	if err := db.QueryRowSQL(ctx, maxQuery).Scan(&maxStr); err != nil {
		return nil, fmt.Errorf("query PK max: %w", err)
	}

	if !minStr.Valid || !maxStr.Valid {
		return nil, fmt.Errorf("table %s is empty", cfg.Table)
	}

	minID := new(big.Int)
	if _, ok := minID.SetString(minStr.String, 10); !ok {
		return nil, fmt.Errorf("parse PK min %q: invalid integer", minStr.String)
	}
	maxID := new(big.Int)
	if _, ok := maxID.SetString(maxStr.String, 10); !ok {
		return nil, fmt.Errorf("parse PK max %q: invalid integer", maxStr.String)
	}

	pkRange := new(big.Int).Sub(maxID, minID)
	if pkRange.Sign() <= 0 {
		return &Plan{
			MinID:       minID,
			MaxID:       maxID,
			ChunkStep:   1,
			TotalChunks: 1,
			ChunkColumn: chunkCol,
		}, nil
	}

	chunkStep := int64(cfg.BatchSize)
	pkRangeInt := pkRange.Int64()
	totalChunks := pkRangeInt / chunkStep
	if pkRangeInt%chunkStep != 0 {
		totalChunks++
	}

	slog.Info("plan generated",
		slog.String("chunk_column", chunkCol),
		slog.String("min_id", minID.String()),
		slog.String("max_id", maxID.String()),
		slog.Int64("total_chunks", totalChunks),
	)

	return &Plan{
		MinID:       minID,
		MaxID:       maxID,
		ChunkStep:   chunkStep,
		TotalChunks: totalChunks,
		ChunkColumn: chunkCol,
	}, nil
}

func detectPKColumns(ctx context.Context, db *config.LogDB, cfg *config.Config) ([]string, error) {
	query := db.Config().Annotate(
		"SELECT COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE " +
			"WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND CONSTRAINT_NAME = 'PRIMARY' " +
			"ORDER BY ORDINAL_POSITION",
	)
	rows, err := db.QuerySQL(ctx, query, cfg.Database, cfg.Table)
	if err != nil {
		return nil, fmt.Errorf("detect PK: %w", err)
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func getColumnDataType(ctx context.Context, db *config.LogDB, cfg *config.Config, col string) (string, error) {
	query := db.Config().Annotate(
		"SELECT DATA_TYPE FROM information_schema.COLUMNS " +
			"WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?",
	)
	var dataType string
	if err := db.QueryRowSQL(ctx, query, cfg.Database, cfg.Table, col).Scan(&dataType); err != nil {
		return "", fmt.Errorf("get column type for %s: %w", col, err)
	}
	return dataType, nil
}
