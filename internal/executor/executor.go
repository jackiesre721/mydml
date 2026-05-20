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

	paused      atomic.Bool
	stopped     atomic.Bool
	panicked    atomic.Bool
	totalAff    atomic.Int64
	totalChunks atomic.Int64
	maxLagBits  atomic.Uint64
	startTime   time.Time
}

func NewExecutor(db *config.LogDB, cfg *config.Config, plan *planner.Plan, t *throttler.Throttler,
	mon *monitor.Monitor, rep *reporter.Reporter) *Executor {
	return &Executor{
		DB:        db,
		Cfg:       cfg,
		Plan:      plan,
		Throttler: t,
		Monitor:   mon,
		Reporter:  rep,
		startTime: time.Now(),
	}
}

func (e *Executor) IsPaused() bool  { return e.paused.Load() }
func (e *Executor) IsStopped() bool { return e.stopped.Load() }
func (e *Executor) Pause()         { e.paused.Store(true) }
func (e *Executor) Resume()        { e.paused.Store(false) }
func (e *Executor) Stop()          { e.stopped.Store(true) }
func (e *Executor) Panic()         { e.panicked.Store(true) }

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
		if e.panicked.Load() {
			return nil, fmt.Errorf("panic signal received")
		}
		if e.stopped.Load() {
			e.Reporter.Warn("stop signal received, completing current chunk")
			break
		}
		for e.paused.Load() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(1 * time.Second):
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Check critical load
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

		curStart := chunkStart
		chunkDuration := time.Duration(0)
		var chunkAffected int64

		if e.Cfg.DryRun {
			var count int64
			query := fmt.Sprintf(
				"SELECT COUNT(*) FROM `%s` WHERE `%s` >= %d AND `%s` < %d AND %s",
				table, chunkCol, curStart, chunkCol, chunkEnd, where,
			)
			startTime := time.Now()
			if err := e.DB.QueryRowSQL(ctx, query).Scan(&count); err != nil {
				e.Reporter.Error("dry-run query failed at chunk %d [%d,%d): %v | sql=%.200s", chunkIndex, curStart, chunkEnd, err, query)
				chunkStart += e.Plan.ChunkStep
				chunkIndex++
				continue
			}
			chunkDuration = time.Since(startTime)
			chunkAffected = count
			e.totalAff.Add(count)
			e.totalChunks.Add(1)
		} else {
			chunkStartTime := time.Now()
			query := e.buildChunkSQL(table, chunkCol, curStart, chunkEnd, where)
			result, err := e.DB.ExecSQL(ctx, query)
			if err != nil {
				e.Reporter.Error("%s failed at chunk %d [%d,%d): %v | sql=%.200s", e.Cfg.Mode, chunkIndex, curStart, chunkEnd, err, query)
			} else {
				chunkAffected, _ = result.RowsAffected()
				e.totalAff.Add(chunkAffected)
				e.totalChunks.Add(1)
			}
			chunkDuration = time.Since(chunkStartTime)
		}

		lag, _ := e.Monitor.CheckReplicationLag()
		e.updateMaxLag(lag)

		sleepDur := e.Throttler.ComputeSleep(chunkAffected, chunkDuration)
		sleepMs := int(sleepDur.Milliseconds())

		totalAff := e.totalAff.Load()
		e.Reporter.ChunkCompleted(reporter.ChunkFields{
			ChunkIndex:    chunkIndex,
			TotalChunks:   e.Plan.TotalChunks,
			ChunkStart:    curStart,
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

		chunkStart += e.Plan.ChunkStep
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
