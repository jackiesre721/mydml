package config

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string

	Table string
	Where string
	Set   string // SET clause for UPDATE mode

	SourceTable string // Source table for INSERT_SELECT mode
	TargetTable string // Target table for INSERT_SELECT mode
	Columns     string // Optional column list for INSERT_SELECT

	Mode string // "delete", "update", or "insert_select"

	BatchSize int
	SleepMs   int
	MaxLagSec int

	NiceRatio     float64
	MaxLoad       string
	CriticalLoad  string
	ThrottleQuery string
	CheckSlaveLag []string // host:port list for replica lag checking

	DryRun  bool
	MaxRows int64

	ControlAddr string
	TaskID      string

	Verbose bool
	LogFile string
}

func Default() *Config {
	return &Config{
		Host:        "127.0.0.1",
		Port:        3306,
		BatchSize:   500,
		SleepMs:     100,
		MaxLagSec:   1,
		ControlAddr: "127.0.0.1:8080",
	}
}

func (c *Config) Validate() error {
	if c.Host == "" {
		return fmt.Errorf("host is required")
	}
	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if c.User == "" {
		return fmt.Errorf("user is required")
	}
	if c.Database == "" {
		return fmt.Errorf("database is required")
	}
	if c.Table == "" {
		return fmt.Errorf("table is required")
	}
	if c.Where == "" {
		return fmt.Errorf("where condition is required (safety: unconditional operations are not allowed)")
	}
	if c.Mode == "update" && c.Set == "" {
		return fmt.Errorf("--set is required for update mode (e.g. --set \"status = 'archived'\")")
	}
	if c.Mode == "insert_select" {
		if c.SourceTable == "" {
			return fmt.Errorf("--source-table is required for insert-select mode")
		}
		if c.TargetTable == "" {
			return fmt.Errorf("--target-table is required for insert-select mode")
		}
		if c.SourceTable == c.TargetTable {
			return fmt.Errorf("source and target tables must be different")
		}
	}
	// WHERE constraints: no subqueries, no LIMIT
	if err := validateWhere(c.Where); err != nil {
		return err
	}
	if c.BatchSize < 100 || c.BatchSize > 5000 {
		return fmt.Errorf("batch_size must be between 100 and 5000")
	}
	if c.SleepMs < 0 || c.SleepMs > 10000 {
		return fmt.Errorf("sleep_ms must be between 0 and 10000")
	}
	if c.MaxLagSec < 1 || c.MaxLagSec > 60 {
		return fmt.Errorf("max_lag_sec must be between 1 and 60")
	}
	if c.NiceRatio < 0 || c.NiceRatio > 5.0 {
		return fmt.Errorf("nice_ratio must be between 0.0 and 5.0")
	}
	return nil
}

func (c *Config) GetTaskID() string {
	if c.TaskID != "" {
		return c.TaskID
	}
	h := sha256.Sum256([]byte(c.Where))
	ts := time.Now().Format("20060102150405")
	return fmt.Sprintf("%s_%x_%s", c.Table, h[:4], ts)
}

func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?parseTime=true&timeout=10s&readTimeout=30s&writeTimeout=30s",
		c.User, c.Password, c.Host, c.Port, c.Database,
	)
}

func (c *Config) Annotate(sql string) string {
	return fmt.Sprintf("/* mydml:task=%s */ %s", c.GetTaskID(), sql)
}

func (c *Config) SetupPool(db *sql.DB) {
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(15 * time.Minute)
}

func validateWhere(where string) error {
	upper := strings.ToUpper(where)

	if strings.Contains(upper, "SELECT") {
		return fmt.Errorf("WHERE clause cannot contain subqueries (SELECT found)")
	}
	limitRe := regexp.MustCompile(`(?i)\bLIMIT\b`)
	if limitRe.MatchString(where) {
		return fmt.Errorf("WHERE clause cannot contain LIMIT (the tool adds LIMIT internally per batch)")
	}
	return nil
}
