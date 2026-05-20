package pipeline

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/control"
	"github.com/jackiesre721/mydml/internal/executor"
	"github.com/jackiesre721/mydml/internal/monitor"
	"github.com/jackiesre721/mydml/internal/planner"
	"github.com/jackiesre721/mydml/internal/reporter"
	"github.com/jackiesre721/mydml/internal/throttler"
)

func Run(cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	rep := reporter.New(cfg)

	// Connect MySQL
	db, err := sql.Open("mysql", cfg.DSN())
	if err != nil {
		return fmt.Errorf("connect to MySQL: %w", err)
	}
	defer db.Close()
	cfg.SetupPool(db)

	if err = db.Ping(); err != nil {
		return fmt.Errorf("ping MySQL: %w", err)
	}
	rep.Logger().Info("connected to MySQL", slog.String("host", cfg.Host), slog.Int("port", cfg.Port))

	// Pre-checks
	logDB := config.NewLogDB(db, cfg)
	mon := monitor.New(db, cfg)
	defer mon.Close()
	preResult, err := mon.RunPreChecks()
	if err != nil {
		return fmt.Errorf("pre-checks failed: %w", err)
	}
	for _, w := range preResult.Warnings {
		rep.Warn(w)
	}
	if len(preResult.Errors) > 0 {
		for _, e := range preResult.Errors {
			rep.Error(e)
		}
		return fmt.Errorf("pre-checks failed, cannot proceed")
	}

	// Plan (PK detection + MIN/MAX)
	plan, err := planner.New(logDB, cfg)
	if err != nil {
		rep.Error("plan generation failed: %v", err)
		return fmt.Errorf("plan generation failed: %w", err)
	}
	rep.Logger().Info("execution plan generated",
		slog.String("chunk_column", plan.ChunkColumn),
		slog.Int64("min_id", plan.MinID),
		slog.Int64("max_id", plan.MaxID),
		slog.Int64("total_chunks", plan.TotalChunks),
	)

	// Context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		rep.Warn("received signal %s, gracefully stopping...", sig)
		cancel()
	}()

	// Create executor and throttler
	t := throttler.New(cfg, mon)
	e := executor.NewExecutor(logDB, cfg, plan, t, mon, rep)

	// Start control server
	srv := control.New(cfg.ControlAddr, cfg, e)
	go func() {
		if srvErr := srv.Start(ctx); srvErr != nil {
			rep.Warn("control server error: %v", srvErr)
		}
	}()
	rep.Logger().Info("control server started", slog.String("addr", cfg.ControlAddr))

	// Execute
	rep.TaskStarted(plan.ChunkColumn)

	stats, err := e.Execute(ctx)
	if err != nil {
		if ctx.Err() != nil {
			rep.Warn("task interrupted by signal")
		} else {
			return fmt.Errorf("execution failed: %w", err)
		}
	}

	// Final report
	if stats != nil {
		duration := time.Since(stats.StartTime)
		var avgSpeed float64
		if duration.Seconds() > 0 {
			avgSpeed = float64(stats.TotalAffected) / duration.Seconds()
		}
		rep.TaskCompleted(reporter.Summary{
			Table:         cfg.Table,
			Condition:     cfg.Where,
			ChunkColumn:   plan.ChunkColumn,
			Mode:          cfg.Mode,
			Duration:      duration,
			TotalAffected: stats.TotalAffected,
			TotalChunks:   stats.TotalChunks,
			AvgSpeed:      avgSpeed,
			MaxReplLag:    stats.MaxReplLag,
		})
	}

	return nil
}
