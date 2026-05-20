package executor

import (
	"context"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/monitor"
	"github.com/jackiesre721/mydml/internal/planner"
	"github.com/jackiesre721/mydml/internal/reporter"
	"github.com/jackiesre721/mydml/internal/throttler"
)

type Stats struct {
	TotalAffected int64
	TotalChunks   int64
	MaxReplLag    float64
	StartTime     time.Time
}

type Executor struct {
	DB        *config.LogDB
	Cfg       *config.Config
	Plan      *planner.Plan
	Throttler *throttler.Throttler
	Monitor   *monitor.Monitor
	Reporter  *reporter.Reporter

	paused          atomic.Bool
	stopped         atomic.Bool
	panicked        atomic.Bool
	totalAff        atomic.Int64
	totalChunks     atomic.Int64
	maxLagBits      atomic.Uint64
	consecutiveFail atomic.Int64
	startTime       time.Time

	maxRetries       int
	maxConsecFails   int64
}

func NewExecutor(db *config.LogDB, cfg *config.Config, plan *planner.Plan, t *throttler.Throttler,
	mon *monitor.Monitor, rep *reporter.Reporter) *Executor {
	return &Executor{
		DB:             db,
		Cfg:            cfg,
		Plan:           plan,
		Throttler:      t,
		Monitor:        mon,
		Reporter:       rep,
		startTime:      time.Now(),
		maxRetries:     3,
		maxConsecFails: 10,
	}
}

func (e *Executor) IsPaused() bool  { return e.paused.Load() }
func (e *Executor) IsStopped() bool { return e.stopped.Load() }
func (e *Executor) Pause()          { e.paused.Store(true) }
func (e *Executor) Resume()         { e.paused.Store(false) }
func (e *Executor) Stop()           { e.stopped.Store(true) }
func (e *Executor) Panic()          { e.panicked.Store(true) }

func (e *Executor) updateMaxLag(lag float64) {
	for {
		old := math.Float64frombits(e.maxLagBits.Load())
		if lag <= old {
			return
		}
		if e.maxLagBits.CompareAndSwap(math.Float64bits(old), math.Float64bits(lag)) {
			return
		}
	}
}

func (e *Executor) loadMaxLag() float64 {
	return math.Float64frombits(e.maxLagBits.Load())
}

func (e *Executor) GetStats() Stats {
	return Stats{
		TotalAffected: e.totalAff.Load(),
		TotalChunks:   e.totalChunks.Load(),
		MaxReplLag:    e.loadMaxLag(),
		StartTime:     e.startTime,
	}
}

func (e *Executor) shouldStop() bool {
	if e.panicked.Load() {
		return true
	}
	if e.stopped.Load() {
		e.Reporter.Warn("stop signal received, completing current chunk")
		return true
	}
	return false
}

func (e *Executor) waitWhilePaused(ctx context.Context) error {
	for e.paused.Load() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

func (e *Executor) executeChunk(ctx context.Context, chunkCol, table, where string, chunkIndex, curStart, chunkEnd int64) (int64, time.Duration) {
	if e.Cfg.DryRun {
		return e.executeDryRunChunk(ctx, chunkCol, table, where, chunkIndex, curStart, chunkEnd)
	}
	return e.executeRealChunk(ctx, chunkCol, table, where, chunkIndex, curStart, chunkEnd)
}

func (e *Executor) executeDryRunChunk(ctx context.Context, chunkCol, table, where string, chunkIndex, curStart, chunkEnd int64) (int64, time.Duration) {
	query := fmt.Sprintf(
		"SELECT COUNT(*) FROM `%s` WHERE `%s` >= %d AND `%s` < %d AND %s",
		table, chunkCol, curStart, chunkCol, chunkEnd, where,
	)
	startTime := time.Now()
	var count int64
	if err := e.DB.QueryRowSQL(ctx, query).Scan(&count); err != nil {
		e.Reporter.Error("dry-run query failed at chunk %d [%d,%d): %v | sql=%.200s", chunkIndex, curStart, chunkEnd, err, query)
		return 0, 0
	}
	e.totalAff.Add(count)
	e.totalChunks.Add(1)
	return count, time.Since(startTime)
}

func (e *Executor) executeRealChunk(ctx context.Context, chunkCol, table, where string, chunkIndex, curStart, chunkEnd int64) (int64, time.Duration) {
	startTime := time.Now()
	query := e.buildChunkSQL(table, chunkCol, curStart, chunkEnd, where)

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(attempt*attempt) * time.Second
			e.Reporter.Warn("retrying chunk %d [%d,%d), attempt %d, backoff %v", chunkIndex, curStart, chunkEnd, attempt+1, backoff)
			if err := e.Throttler.Sleep(ctx, backoff); err != nil {
				return 0, time.Since(startTime)
			}
		}

		result, err := e.DB.ExecSQL(ctx, query)
		if err != nil {
			e.Reporter.Error("%s failed at chunk %d [%d,%d): %v | sql=%.200s", e.Cfg.Mode, chunkIndex, curStart, chunkEnd, err, query)
			continue
		}
		affected, _ := result.RowsAffected()
		e.totalAff.Add(affected)
		e.totalChunks.Add(1)
		e.consecutiveFail.Store(0)
		return affected, time.Since(startTime)
	}

	e.Reporter.Error("chunk %d [%d,%d) failed after %d retries", chunkIndex, curStart, chunkEnd, e.maxRetries)
	e.consecutiveFail.Add(1)
	return 0, time.Since(startTime)
}

func (e *Executor) Execute(ctx context.Context) (*Stats, error) {
	if _, err := e.DB.ExecSQL(ctx, "SET SESSION innodb_lock_wait_timeout = 5"); err != nil {
		return nil, fmt.Errorf("set lock_wait_timeout: %w", err)
	}
	if _, err := e.DB.ExecSQL(ctx, "SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ"); err != nil {
		return nil, fmt.Errorf("set isolation level: %w", err)
	}

	chunkCol := e.Plan.ChunkColumn
	table := e.Cfg.Table
	where := e.Cfg.Where

	chunkIndex := int64(0)
	for chunkStart := e.Plan.MinID; chunkStart <= e.Plan.MaxID; {
		if e.shouldStop() {
			break
		}
		if err := e.waitWhilePaused(ctx); err != nil {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		if e.Cfg.CriticalLoad != "" {
			if exceeded, _ := e.Monitor.CheckMaxLoad(e.Cfg.CriticalLoad); len(exceeded) > 0 {
				e.Reporter.Warn("critical load exceeded, stopping: %v", exceeded)
				break
			}
		}

		chunkEnd := chunkStart + e.Plan.ChunkStep
		if chunkEnd > e.Plan.MaxID {
			chunkEnd = e.Plan.MaxID + 1
		}

		chunkAffected, chunkDuration := e.executeChunk(ctx, chunkCol, table, where, chunkIndex, chunkStart, chunkEnd)

		if fails := e.consecutiveFail.Load(); fails >= e.maxConsecFails {
			e.Reporter.Error("too many consecutive failures (%d), aborting", fails)
			break
		}

		lag, _ := e.Monitor.CheckReplicationLag()
		e.updateMaxLag(lag)

		sleepDur := e.Throttler.ComputeSleep(chunkAffected, chunkDuration)
		sleepMs := int(sleepDur.Milliseconds())

		totalAff := e.totalAff.Load()
		e.Reporter.ChunkCompleted(reporter.ChunkFields{
			ChunkIndex:    chunkIndex,
			TotalChunks:   e.Plan.TotalChunks,
			ChunkStart:    chunkStart,
			ChunkEnd:      chunkEnd,
			Affected:      chunkAffected,
			TotalAffected: totalAff,
			ChunkDuration: chunkDuration,
			ReplLag:       lag,
			SleepMs:       sleepMs,
		})

		if err := e.Throttler.Sleep(ctx, sleepDur); err != nil {
			return nil, err
		}

		if e.Cfg.MaxRows > 0 && totalAff >= e.Cfg.MaxRows {
			e.Reporter.Warn("max rows limit reached (%d >= %d), stopping", totalAff, e.Cfg.MaxRows)
			break
		}

		nextStart := chunkStart + e.Plan.ChunkStep
		if nextStart <= chunkStart {
			break
		}
		chunkStart = nextStart
		chunkIndex++
	}

	stats := e.GetStats()
	return &stats, nil
}

func (e *Executor) buildChunkSQL(table, chunkCol string, chunkStart, chunkEnd int64, where string) string {
	if e.Cfg.Mode == "update" {
		return fmt.Sprintf(
			"UPDATE `%s` SET %s WHERE `%s` >= %d AND `%s` < %d AND %s",
			table, e.Cfg.Set, chunkCol, chunkStart, chunkCol, chunkEnd, where,
		)
	}
	if e.Cfg.Mode == "insert_select" {
		colClause := "*"
		colInsert := ""
		if e.Cfg.Columns != "" {
			cols := e.Cfg.Columns
			colClause = cols
			colInsert = fmt.Sprintf(" (%s)", cols)
		}
		return fmt.Sprintf(
			"INSERT INTO `%s`%s SELECT %s FROM `%s` WHERE `%s` >= %d AND `%s` < %d AND %s",
			e.Cfg.TargetTable, colInsert, colClause, table, chunkCol, chunkStart, chunkCol, chunkEnd, where,
		)
	}
	return fmt.Sprintf(
		"DELETE FROM `%s` WHERE `%s` >= %d AND `%s` < %d AND %s",
		table, chunkCol, chunkStart, chunkCol, chunkEnd, where,
	)
}
