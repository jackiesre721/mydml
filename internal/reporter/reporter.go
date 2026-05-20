package reporter

import (
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"time"

	"github.com/jackiesre721/mydml/internal/config"
)

type Reporter struct {
	cfg    *config.Config
	logger *slog.Logger
	start  time.Time
}

type ChunkFields struct {
	ChunkIndex    int64
	TotalChunks   int64
	ChunkStart    *big.Int
	ChunkEnd      *big.Int
	Affected      int64
	TotalAffected int64
	ReplLag       float64
	SleepMs       int
	ChunkDuration time.Duration
}

type Summary struct {
	Table         string
	Condition     string
	ChunkColumn   string
	Mode          string
	Duration      time.Duration
	TotalAffected int64
	TotalChunks   int64
	AvgSpeed      float64
	MaxReplLag    float64
}

func New(cfg *config.Config) *Reporter {
	var handler slog.Handler
	if cfg.LogFile != "" {
		f, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN cannot open log file %s: %v, using stdout\n", cfg.LogFile, err)
			handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})
		} else {
			handler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: slog.LevelDebug})
		}
	} else {
		lvl := slog.LevelInfo
		if cfg.Verbose {
			lvl = slog.LevelDebug
		}
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return &Reporter{
		cfg:    cfg,
		logger: logger,
		start:  time.Now(),
	}
}

func (r *Reporter) Logger() *slog.Logger {
	return r.logger
}

func (r *Reporter) TaskStarted(chunkColumn string) {
	r.logger.Info("task started",
		slog.String("task_id", r.cfg.GetTaskID()),
		slog.String("table", r.cfg.Table),
		slog.String("where", r.cfg.Where),
		slog.String("chunk_column", chunkColumn),
		slog.String("mode", r.cfg.Mode),
		slog.Int("batch_size", r.cfg.BatchSize),
		slog.Int("sleep_ms", r.cfg.SleepMs),
		slog.Float64("nice_ratio", r.cfg.NiceRatio),
		slog.Bool("dry_run", r.cfg.DryRun),
	)
}

func (r *Reporter) ChunkCompleted(f ChunkFields) {
	pct := float64(0)
	if f.TotalChunks > 0 {
		pct = float64(f.ChunkIndex) / float64(f.TotalChunks) * 100
	}
	speed := float64(0)
	elapsed := time.Since(r.start)
	if elapsed.Seconds() > 0 {
		speed = float64(f.TotalAffected) / elapsed.Seconds()
	}
	eta := time.Duration(0)
	if speed > 0 && f.TotalChunks > 0 {
		remaining := float64(f.TotalChunks-f.ChunkIndex) / float64(f.TotalChunks) * elapsed.Seconds()
		eta = time.Duration(remaining) * time.Second
	}

	actionLabel := "deleted"
	if r.cfg.Mode == "update" {
		actionLabel = "updated"
	}

	r.logger.Info("chunk completed",
		slog.String("task_id", r.cfg.GetTaskID()),
		slog.String("table", r.cfg.Table),
		slog.Int64("chunk", f.ChunkIndex),
		slog.Int64("total_chunks", f.TotalChunks),
		slog.Float64("percent", pct),
		slog.String("range_start", f.ChunkStart.String()),
		slog.String("range_end", f.ChunkEnd.String()),
		slog.Int64("affected", f.Affected),
		slog.Int64("total_affected", f.TotalAffected),
		slog.Float64("speed_rows_per_sec", speed),
		slog.Float64("repl_lag_sec", f.ReplLag),
		slog.Int("sleep_ms", f.SleepMs),
		slog.String("eta", eta.Round(time.Second).String()),
	)

	fmt.Fprintf(os.Stderr, "\r[%s] Chunk %d/%d (%.1f%%) | %s: %d | Total: %d | Speed: %.0f rows/s | ETA: %s   ",
		time.Now().Format("15:04:05"),
		f.ChunkIndex, f.TotalChunks, pct,
		actionLabel, f.Affected, f.TotalAffected, speed,
		eta.Round(time.Second),
	)
}

func (r *Reporter) TaskCompleted(s Summary) {
	fmt.Fprintln(os.Stderr)

	actionLabel := "deleted"
	if s.Mode == "update" {
		actionLabel = "updated"
	}

	r.logger.Info("task completed",
		slog.String("task_id", r.cfg.GetTaskID()),
		slog.String("table", s.Table),
		slog.String("condition", s.Condition),
		slog.String("chunk_column", s.ChunkColumn),
		slog.String("mode", s.Mode),
		slog.String("duration", s.Duration.Round(time.Second).String()),
		slog.Int64("total_affected", s.TotalAffected),
		slog.Int64("total_chunks", s.TotalChunks),
		slog.Float64("avg_speed_rows_per_sec", s.AvgSpeed),
		slog.Float64("max_repl_lag_sec", s.MaxReplLag),
	)

	fmt.Fprintf(os.Stderr, "\n========== Summary ==========\n")
	fmt.Fprintf(os.Stderr, "Table:       %s\n", s.Table)
	fmt.Fprintf(os.Stderr, "Condition:   %s\n", s.Condition)
	fmt.Fprintf(os.Stderr, "Mode:        %s\n", s.Mode)
	fmt.Fprintf(os.Stderr, "Chunk Col:   %s\n", s.ChunkColumn)
	fmt.Fprintf(os.Stderr, "Duration:    %s\n", s.Duration.Round(time.Second))
	fmt.Fprintf(os.Stderr, "Total:       %d rows %s\n", s.TotalAffected, actionLabel)
	fmt.Fprintf(os.Stderr, "Chunks:      %d\n", s.TotalChunks)
	fmt.Fprintf(os.Stderr, "Avg Speed:   %.0f rows/s\n", s.AvgSpeed)
	fmt.Fprintf(os.Stderr, "Max Repl Lag: %.1fs\n", s.MaxReplLag)
	fmt.Fprintf(os.Stderr, "====================================\n")
}

func (r *Reporter) Warn(msg string, args ...any) {
	r.logger.Warn(fmt.Sprintf(msg, args...))
}

func (r *Reporter) Error(msg string, args ...any) {
	r.logger.Error(fmt.Sprintf(msg, args...))
}
