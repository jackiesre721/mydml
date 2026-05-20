package throttler

import (
	"context"
	"math"
	"sync/atomic"
	"time"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/monitor"
)

type Throttler struct {
	cfg       *config.Config
	monitor   *monitor.Monitor
	baseMs    atomic.Int64
	niceRatio atomic.Value // float64 stored as atomic.Value
}

func New(cfg *config.Config, mon *monitor.Monitor) *Throttler {
	t := &Throttler{
		cfg:     cfg,
		monitor: mon,
	}
	t.baseMs.Store(int64(cfg.SleepMs))
	t.niceRatio.Store(cfg.NiceRatio)
	return t
}

func (t *Throttler) UpdateSleep(ms int64) {
	t.baseMs.Store(ms)
}

func (t *Throttler) UpdateNiceRatio(ratio float64) {
	t.niceRatio.Store(ratio)
}

func (t *Throttler) getNiceRatio() float64 {
	v := t.niceRatio.Load()
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

func (t *Throttler) ComputeSleep(affectedRows int64, chunkDuration time.Duration) time.Duration {
	// affected_rows == 0: sparse chunk, skip sleep
	if affectedRows == 0 {
		return 0
	}

	// Nice ratio takes priority
	niceRatio := t.getNiceRatio()
	if niceRatio > 0 {
		return time.Duration(float64(chunkDuration) * niceRatio)
	}

	baseMs := t.baseMs.Load()
	multiplier := 1.0

	// Dimension 1: replication lag
	lag, err := t.monitor.CheckReplicationLag()
	if err == nil {
		switch {
		case lag > float64(t.cfg.MaxLagSec)*3:
			multiplier = math.Max(multiplier, 4.0)
		case lag > float64(t.cfg.MaxLagSec):
			multiplier = math.Max(multiplier, 2.0)
		}
	}

	// Dimension 2: lock waits
	lockWaits, err := t.monitor.CheckLockWaits()
	if err == nil {
		switch {
		case lockWaits > 20:
			multiplier = math.Max(multiplier, 4.0)
		case lockWaits > 5:
			multiplier = math.Max(multiplier, 2.0)
		}
	}

	// Dimension 2.5: max-load thresholds
	if t.cfg.MaxLoad != "" {
		if exceeded, _ := t.monitor.CheckMaxLoad(t.cfg.MaxLoad); len(exceeded) > 0 {
			multiplier = math.Max(multiplier, 3.0)
		}
	}

	// Dimension 2.6: custom throttle query
	if t.cfg.ThrottleQuery != "" {
		if val, _ := t.monitor.CheckThrottleQuery(t.cfg.ThrottleQuery); val > 0 {
			multiplier = math.Max(multiplier, 2.0)
		}
	}

	// Dimension 3: affected_rows feedback
	switch {
	case affectedRows < 100:
		multiplier *= 0.5
	case affectedRows > 500:
		multiplier *= 1.5
	}

	sleepMs := float64(baseMs) * multiplier
	if sleepMs < 10 {
		sleepMs = 10
	}
	return time.Duration(sleepMs) * time.Millisecond
}

func (t *Throttler) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
