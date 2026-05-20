package main

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/pipeline"
)

var cfg = config.Default()

func main() {
	rootCmd := &cobra.Command{
		Use:   "mydml",
		Short: "MySQL large-scale DML tool (batch delete/update without locking)",
	}

	deleteCmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete rows from a table in small batches",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Mode = "delete"
			return pipeline.Run(cfg)
		},
	}

	updateCmd := &cobra.Command{
		Use:   "update",
		Short: "Update rows in a table in small batches",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Mode = "update"
			return pipeline.Run(cfg)
		},
	}

	insertSelectCmd := &cobra.Command{
		Use:   "insert-select",
		Short: "Insert rows from source table to target table in small batches",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg.Mode = "insert_select"
			cfg.Table = cfg.SourceTable
			return pipeline.Run(cfg)
		},
	}

	addCommonFlags(deleteCmd)
	addCommonFlags(updateCmd)
	addCommonFlags(insertSelectCmd)

	updateCmd.Flags().StringVar(&cfg.Set, "set", "", "SET clause for UPDATE (e.g. \"status = 'archived'\")")
	updateCmd.MarkFlagRequired("set")

	insertSelectCmd.Flags().StringVar(&cfg.SourceTable, "source-table", "", "Source table to read from (required)")
	insertSelectCmd.Flags().StringVar(&cfg.TargetTable, "target-table", "", "Target table to insert into (required)")
	insertSelectCmd.Flags().StringVar(&cfg.Columns, "columns", "", "Column list (auto-detected if empty)")
	insertSelectCmd.MarkFlagRequired("source-table")
	insertSelectCmd.MarkFlagRequired("target-table")
	insertSelectCmd.MarkFlagRequired("where")

	deleteCmd.MarkFlagRequired("table")
	deleteCmd.MarkFlagRequired("where")
	updateCmd.MarkFlagRequired("table")
	updateCmd.MarkFlagRequired("where")

	rootCmd.AddCommand(deleteCmd, updateCmd, insertSelectCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func addCommonFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&cfg.Host, "host", cfg.Host, "MySQL host")
	cmd.Flags().IntVar(&cfg.Port, "port", cfg.Port, "MySQL port")
	cmd.Flags().StringVar(&cfg.User, "user", cfg.User, "MySQL user")
	cmd.Flags().StringVar(&cfg.Password, "password", cfg.Password, "MySQL password")
	cmd.Flags().StringVar(&cfg.Database, "database", cfg.Database, "MySQL database")

	cmd.Flags().StringVar(&cfg.Table, "table", "", "Target table name (required)")
	cmd.Flags().StringVar(&cfg.Where, "where", "", "WHERE condition without WHERE keyword (required)")

	cmd.Flags().IntVar(&cfg.BatchSize, "batch-size", cfg.BatchSize, "Rows per batch (100-5000)")
	cmd.Flags().IntVar(&cfg.SleepMs, "sleep-ms", cfg.SleepMs, "Sleep between batches in ms")
	cmd.Flags().IntVar(&cfg.MaxLagSec, "max-lag-sec", cfg.MaxLagSec, "Max replication lag in seconds")

	cmd.Flags().Float64Var(&cfg.NiceRatio, "nice-ratio", cfg.NiceRatio, "Work:sleep ratio (0=full speed)")
	cmd.Flags().StringVar(&cfg.MaxLoad, "max-load", "", "Max load thresholds, e.g. Threads_running=25")
	cmd.Flags().StringVar(&cfg.CriticalLoad, "critical-load", "", "Critical load thresholds")
	cmd.Flags().StringVar(&cfg.ThrottleQuery, "throttle-query", "", "Custom throttle SQL")
	cmd.Flags().StringSliceVar(&cfg.CheckSlaveLag, "check-slave-lag", nil, "Replica host:port to check lag (repeatable)")

	cmd.Flags().BoolVar(&cfg.DryRun, "dry-run", false, "Dry-run mode (count only, no changes)")
	cmd.Flags().Int64Var(&cfg.MaxRows, "max-rows", 0, "Max rows to affect (0=unlimited)")

	cmd.Flags().StringVar(&cfg.ControlAddr, "control-addr", cfg.ControlAddr, "HTTP control API address")
	cmd.Flags().StringVar(&cfg.TaskID, "task-id", "", "Custom task ID (auto-generated if empty)")

	cmd.Flags().BoolVar(&cfg.Verbose, "verbose", false, "Verbose logging")
	cmd.Flags().StringVar(&cfg.LogFile, "log-file", "", "Log file path (default: stdout)")

	bindEnvVars(cmd)
}

func bindEnvVars(cmd *cobra.Command) {
	envVars := map[string]string{
		"host":     "MYSQL_DELETE_HOST",
		"port":     "MYSQL_DELETE_PORT",
		"user":     "MYSQL_DELETE_USER",
		"password": "MYSQL_DELETE_PASSWORD",
		"database": "MYSQL_DELETE_DATABASE",
		"table":    "MYSQL_DELETE_TABLE",
	}
	for flag, env := range envVars {
		if val := os.Getenv(env); val != "" {
			cmd.Flags().Set(flag, val)
		}
	}
}
