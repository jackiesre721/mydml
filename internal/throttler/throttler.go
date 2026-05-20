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
	if affectedRows == 0 {
		return 0
	}

	niceRatio := t.getNiceRatio()
	if niceRatio > 0 {
		return time.Duration(float64(chunkDuration) * niceRatio)
	}

	multiplier := t.computeMultiplier(affectedRows)
	sleepMs := float64(t.baseMs.Load()) * multiplier
	if sleepMs < 10 {
		sleepMs = 10
	}
	return time.Duration(sleepMs) * time.Millisecond
}

func (t *Throttler) computeMultiplier(affectedRows int64) float64 {
	m := 1.0

	// Replication lag
	if lag, err := t.monitor.CheckReplicationLag(); err == nil {
		switch {
		case lag > float64(t.cfg.MaxLagSec)*3:
			m = math.Max(m, 4.0)
		case lag > float64(t.cfg.MaxLagSec):
			m = math.Max(m, 2.0)
		}
	}

	// Lock waits
	if lockWaits, err := t.monitor.CheckLockWaits(); err == nil {
		switch {
		case lockWaits > 20:
			m = math.Max(m, 4.0)
		case lockWaits > 5:
			m = math.Max(m, 2.0)
		}
	}

	// Max-load thresholds
	if t.cfg.MaxLoad != "" {
		if exceeded, _ := t.monitor.CheckMaxLoad(t.cfg.MaxLoad); len(exceeded) > 0 {
			m = math.Max(m, 3.0)
		}
	}

	// Custom throttle query
	if t.cfg.ThrottleQuery != "" {
		if val, _ := t.monitor.CheckThrottleQuery(t.cfg.ThrottleQuery); val > 0 {
			m = math.Max(m, 2.0)
		}
	}

	// Affected rows feedback
	switch {
	case affectedRows < 100:
		m *= 0.5
	case affectedRows > 500:
		m *= 1.5
	}

	return m
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
