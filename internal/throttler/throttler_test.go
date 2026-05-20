package throttler

import (
	"context"
	"testing"
	"time"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/monitor"
)

func newTestThrottler(cfg *config.Config) *Throttler {
	// Monitor with no replicas, no DB — lag/lock checks will return 0
	mon := &monitor.Monitor{}
	return New(cfg, mon)
}

func TestComputeSleep_SparseChunk(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 100
	tt := newTestThrottler(cfg)

	// affectedRows == 0: sparse chunk, skip sleep
	dur := tt.ComputeSleep(0, 100*time.Millisecond)
	if dur != 0 {
		t.Errorf("sparse chunk should return 0 sleep, got %v", dur)
	}
}

func TestComputeSleep_NiceRatio(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 100
	cfg.NiceRatio = 2.0
	tt := newTestThrottler(cfg)

	// Nice ratio takes priority: sleep = chunkDuration * ratio
	dur := tt.ComputeSleep(500, 100*time.Millisecond)
	want := 200 * time.Millisecond // 100ms * 2.0
	if dur != want {
		t.Errorf("nice-ratio sleep = %v, want %v", dur, want)
	}
}

func TestComputeSleep_BaseMs(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 200
	cfg.NiceRatio = 0
	tt := newTestThrottler(cfg)

	// 300 rows is in [100, 500] range, multiplier stays 1.0
	dur := tt.ComputeSleep(300, 100*time.Millisecond)
	baseMs := dur.Milliseconds()
	if baseMs != 200 {
		t.Errorf("sleep = %dms, want 200ms (200*1.0)", baseMs)
	}
}

func TestComputeSleep_LargeAffected(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 100
	cfg.NiceRatio = 0
	tt := newTestThrottler(cfg)

	// affectedRows > 500: multiplier = 1.0 * 1.5
	dur := tt.ComputeSleep(600, 100*time.Millisecond)
	baseMs := dur.Milliseconds()
	wantMs := int64(150) // 100 * 1.5
	if baseMs != wantMs {
		t.Errorf("large affected sleep = %dms, want %dms", baseMs, wantMs)
	}
}

func TestComputeSleep_MinimumSleep(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 10
	cfg.NiceRatio = 0
	tt := newTestThrottler(cfg)

	// Even with small affected_rows * small base, minimum is 10ms
	dur := tt.ComputeSleep(50, 10*time.Millisecond)
	if dur < 10*time.Millisecond {
		t.Errorf("sleep should be at least 10ms, got %v", dur)
	}
}

func TestSleep_Cancellable(t *testing.T) {
	cfg := config.Default()
	tt := newTestThrottler(cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := tt.Sleep(ctx, 5*time.Second)
	if err == nil {
		t.Error("expected context cancelled error")
	}
	if ctx.Err() != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", ctx.Err())
	}
}

func TestSleep_ZeroDuration(t *testing.T) {
	cfg := config.Default()
	tt := newTestThrottler(cfg)

	err := tt.Sleep(context.Background(), 0)
	if err != nil {
		t.Errorf("zero duration sleep should return nil, got %v", err)
	}
}

func TestSleep_NormalCompletion(t *testing.T) {
	cfg := config.Default()
	tt := newTestThrottler(cfg)

	start := time.Now()
	err := tt.Sleep(context.Background(), 50*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("sleep should have waited ~50ms, only waited %v", elapsed)
	}
}

func TestUpdateSleep(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 100
	cfg.NiceRatio = 0
	tt := newTestThrottler(cfg)

	tt.UpdateSleep(500)

	// 50 rows triggers < 100 → multiplier *= 0.5
	dur := tt.ComputeSleep(50, 100*time.Millisecond)
	baseMs := dur.Milliseconds()
	if baseMs != 250 {
		t.Errorf("after UpdateSleep(500), sleep = %dms, want 250ms (500*0.5)", baseMs)
	}
}

func TestUpdateNiceRatio(t *testing.T) {
	cfg := config.Default()
	cfg.SleepMs = 100
	cfg.NiceRatio = 0
	tt := newTestThrottler(cfg)

	tt.UpdateNiceRatio(3.0)

	dur := tt.ComputeSleep(500, 100*time.Millisecond)
	want := 300 * time.Millisecond // 100ms * 3.0
	if dur != want {
		t.Errorf("after UpdateNiceRatio(3.0), sleep = %v, want %v", dur, want)
	}
}
