package monitor

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/jackiesre721/mydml/internal/config"
)

type Monitor struct {
	db       *sql.DB
	cfg      *config.Config
	replicas []*sql.DB
}

func New(db *sql.DB, cfg *config.Config) *Monitor {
	m := &Monitor{db: db, cfg: cfg}

	// Connect to replicas for lag checking
	for _, addr := range cfg.CheckSlaveLag {
		dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?parseTime=true&timeout=5s&readTimeout=10s",
			cfg.User, cfg.Password, addr, cfg.Database,
		)
		rdb, err := sql.Open("mysql", dsn)
		if err != nil {
			slog.Warn("failed to connect replica, skipping", "addr", addr, "error", err)
			continue
		}
		rdb.SetMaxOpenConns(1)
		rdb.SetMaxIdleConns(1)
		rdb.SetConnMaxLifetime(15 * time.Minute)

		if err := rdb.Ping(); err != nil {
			slog.Warn("failed to ping replica, skipping", "addr", addr, "error", err)
			rdb.Close()
			continue
		}
		slog.Info("connected to replica for lag checking", "addr", addr)
		m.replicas = append(m.replicas, rdb)
	}

	return m
}

func (m *Monitor) Close() {
	for _, rdb := range m.replicas {
		rdb.Close()
	}
	m.replicas = nil
}

type PreCheckResult struct {
	Warnings []string
	Errors   []string
}

func (m *Monitor) CheckBinlogFormat() (string, error) {
	var name, value string
	err := m.db.QueryRow(m.cfg.Annotate("SHOW VARIABLES LIKE 'binlog_format'")).Scan(&name, &value)
	if err != nil {
		// Variable may not exist in MySQL 9.x+ (ROW-only), treat as safe
		return "ROW", nil
	}
	return value, nil
}

func (m *Monitor) CheckForeignKeys(tableName string) ([]string, error) {
	query := m.cfg.Annotate(
		"SELECT CONSTRAINT_NAME, TABLE_NAME FROM information_schema.REFERENTIAL_CONSTRAINTS " +
			"WHERE UNIQUE_CONSTRAINT_SCHEMA = ? AND REFERENCED_TABLE_NAME = ?")
	rows, err := m.db.Query(query, m.cfg.Database, tableName)
	if err != nil {
		return nil, fmt.Errorf("check foreign keys: %w", err)
	}
	defer rows.Close()

	var children []string
	for rows.Next() {
		var constraintName, tableName string
		if err := rows.Scan(&constraintName, &tableName); err != nil {
			return nil, err
		}
		children = append(children, fmt.Sprintf("%s (constraint: %s)", tableName, constraintName))
	}
	return children, rows.Err()
}

func (m *Monitor) CheckTriggers(eventType string, tableName string) ([]string, error) {
	query := m.cfg.Annotate(
		"SELECT TRIGGER_NAME FROM information_schema.TRIGGERS " +
			"WHERE EVENT_OBJECT_SCHEMA = ? AND EVENT_OBJECT_TABLE = ? AND EVENT_MANIPULATION = ?")
	rows, err := m.db.Query(query, m.cfg.Database, tableName, eventType)
	if err != nil {
		return nil, fmt.Errorf("check triggers: %w", err)
	}
	defer rows.Close()

	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		triggers = append(triggers, name)
	}
	return triggers, rows.Err()
}

func (m *Monitor) RunPreChecks() (*PreCheckResult, error) {
	result := &PreCheckResult{}

	// 1. Binlog format
	format, err := m.CheckBinlogFormat()
	if err != nil {
		return nil, err
	}
	if strings.ToUpper(format) == "STATEMENT" {
		result.Errors = append(result.Errors,
			"binlog_format=STATEMENT detected. DELETE ... LIMIT is non-deterministic in statement-based replication. "+
				"Switch to ROW or MIXED before running this tool.")
	}

	// 2. Foreign keys
	// For INSERT_SELECT, FK check is on target table; otherwise on the operating table.
	fkTable := m.cfg.Table
	if m.cfg.Mode == "insert_select" {
		fkTable = m.cfg.TargetTable
	}
	children, err := m.CheckForeignKeys(fkTable)
	if err != nil {
		return nil, err
	}
	if len(children) > 0 {
		result.Errors = append(result.Errors,
			fmt.Sprintf("table %s is referenced by foreign keys: %s. Cannot proceed.",
				fkTable, strings.Join(children, ", ")))
	}

	// 3. Triggers (warning only)
	triggerTable := m.cfg.Table
	if m.cfg.Mode == "insert_select" {
		triggerTable = m.cfg.TargetTable
	}
	eventType := modeToTriggerEvent(m.cfg.Mode)
	triggers, err := m.CheckTriggers(eventType, triggerTable)
	if err != nil {
		return nil, err
	}
	if len(triggers) > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("table has %s triggers: %s. Each row will fire trigger logic, expect higher overhead.",
				eventType, strings.Join(triggers, ", ")))
	}

	return result, nil
}

// CheckReplicationLag returns the maximum Seconds_Behind_Master across all configured replicas.
func (m *Monitor) CheckReplicationLag() (float64, error) {
	if len(m.replicas) == 0 {
		return 0, nil
	}

	var maxLag float64
	for _, rdb := range m.replicas {
		lag, err := queryLagFromDB(rdb)
		if err != nil {
			slog.Debug("replica lag check failed", "error", err)
			continue
		}
		if lag > maxLag {
			maxLag = lag
		}
	}
	return maxLag, nil
}

func queryLagFromDB(db *sql.DB) (float64, error) {
	// Try SHOW REPLICA STATUS first (MySQL 8.0.22+)
	lag, err := queryLagColumns(db, "SHOW REPLICA STATUS", "Seconds_Behind_Source", "Seconds_Behind_Master")
	if err == nil {
		return lag, nil
	}
	// Fallback to SHOW SLAVE STATUS
	return queryLagColumns(db, "SHOW SLAVE STATUS", "Seconds_Behind_Master")
}

func queryLagColumns(db *sql.DB, query string, lagColumns ...string) (float64, error) {
	rows, err := db.Query(query)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, fmt.Errorf("no rows")
	}

	cols, _ := rows.Columns()
	vals := make([]interface{}, len(cols))
	for i := range vals {
		vals[i] = new(sql.NullString)
	}
	if err := rows.Scan(vals...); err != nil {
		return 0, err
	}

	for _, lagCol := range lagColumns {
		for i, col := range cols {
			if col == lagCol {
				if ns, ok := vals[i].(*sql.NullString); ok && ns.Valid {
					sec, err := strconv.ParseFloat(ns.String, 64)
					if err != nil {
						return 0, fmt.Errorf("parse lag value %q: %w", ns.String, err)
					}
					return sec, nil
				}
			}
		}
	}
	return 0, nil
}

func (m *Monitor) CheckLockWaits() (int, error) {
	if m.db == nil {
		return 0, nil
	}
	var typ, name, status string
	err := m.db.QueryRow(m.cfg.Annotate("SHOW ENGINE INNODB STATUS")).Scan(&typ, &name, &status)
	if err != nil {
		return 0, nil
	}

	return strings.Count(status, "LOCK WAIT"), nil
}

func (m *Monitor) CheckMaxLoad(loadConfig string) (map[string]int64, error) {
	if m.db == nil || loadConfig == "" {
		return nil, nil
	}

	thresholds := parseLoadConfig(loadConfig)
	if len(thresholds) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(thresholds))
	for name := range thresholds {
		names = append(names, "'"+name+"'")
	}

	query := m.cfg.Annotate(
		"SHOW GLOBAL STATUS WHERE Variable_name IN (" + strings.Join(names, ",") + ")")
	rows, err := m.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	exceeded := make(map[string]int64)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			continue
		}
		var val int64
		fmt.Sscanf(value, "%d", &val)
		if threshold, ok := thresholds[name]; ok && val > threshold {
			exceeded[name] = val
		}
	}
	return exceeded, nil
}

func parseLoadConfig(cfg string) map[string]int64 {
	result := make(map[string]int64)
	parts := strings.Split(cfg, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			var val int64
			fmt.Sscanf(kv[1], "%d", &val)
			result[strings.TrimSpace(kv[0])] = val
		}
	}
	return result
}

func (m *Monitor) CheckThrottleQuery(query string) (int64, error) {
	if query == "" {
		return 0, nil
	}
	var val int64
	err := m.db.QueryRow(m.cfg.Annotate(query)).Scan(&val)
	if err != nil {
		return 0, err
	}
	return val, nil
}

func modeToTriggerEvent(mode string) string {
	switch mode {
	case "update":
		return "UPDATE"
	case "insert_select":
		return "INSERT"
	default:
		return "DELETE"
	}
}
