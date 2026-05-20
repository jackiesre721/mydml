package control

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/jackiesre721/mydml/internal/config"
	"github.com/jackiesre721/mydml/internal/executor"
)

type StatusSnapshot struct {
	Status        string  `json:"status"`
	Table         string  `json:"table"`
	ChunkColumn   string  `json:"chunk_column"`
	TotalAffected int64   `json:"total_affected"`
	TotalChunks   int64   `json:"total_chunks"`
	Speed         float64 `json:"speed_rows_per_sec"`
	ReplLag       float64 `json:"repl_lag_sec"`
	DryRun        bool    `json:"dry_run"`
}

type RuntimeConfig struct {
	SleepMs   int     `json:"sleep_ms"`
	MaxLagSec int     `json:"max_lag_sec"`
	NiceRatio float64 `json:"nice_ratio"`
}

type Server struct {
	httpServer *http.Server
	d          *executor.Executor
	cfg        *config.Config
	mu         sync.RWMutex
	runtimeCfg RuntimeConfig
}

func New(addr string, cfg *config.Config, d *executor.Executor) *Server {
	s := &Server{
		cfg: cfg,
		d:   d,
		runtimeCfg: RuntimeConfig{
			SleepMs:   cfg.SleepMs,
			MaxLagSec: cfg.MaxLagSec,
			NiceRatio: cfg.NiceRatio,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pause", s.handlePause)
	mux.HandleFunc("/api/v1/resume", s.handleResume)
	mux.HandleFunc("/api/v1/status", s.handleStatus)
	mux.HandleFunc("/api/v1/stop", s.handleStop)
	mux.HandleFunc("/api/v1/panic", s.handlePanic)
	mux.HandleFunc("/api/v1/config", s.handleConfig)

	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}
	return s
}

func (s *Server) Start(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(shutdownCtx)
	}()

	if err := s.httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) jsonResp(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(data)
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	s.d.Pause()
	s.jsonResp(w, 200, map[string]string{"status": "paused"})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	s.d.Resume()
	s.jsonResp(w, 200, map[string]string{"status": "running"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	stats := s.d.GetStats()
	status := "running"
	if s.d.IsPaused() {
		status = "paused"
	}
	if s.d.IsStopped() {
		status = "stopping"
	}

	snapshot := StatusSnapshot{
		Status:        status,
		Table:         s.cfg.Table,
		TotalAffected: stats.TotalAffected,
		TotalChunks:   stats.TotalChunks,
		ReplLag:       stats.MaxReplLag,
		DryRun:        s.cfg.DryRun,
	}

	s.mu.RLock()
	rcfg := s.runtimeCfg
	s.mu.RUnlock()

	s.jsonResp(w, 200, map[string]interface{}{
		"status": snapshot,
		"config": rcfg,
	})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	s.d.Stop()
	s.jsonResp(w, 200, map[string]string{"status": "stopping", "message": "completing current chunk..."})
}

func (s *Server) handlePanic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
		return
	}
	s.d.Panic()
	s.jsonResp(w, 200, map[string]string{"status": "panic", "message": "immediate termination"})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut {
		var newCfg RuntimeConfig
		if err := json.NewDecoder(r.Body).Decode(&newCfg); err != nil {
			s.jsonResp(w, 400, map[string]string{"error": "invalid json"})
			return
		}
		s.mu.Lock()
		if newCfg.SleepMs >= 0 {
			s.runtimeCfg.SleepMs = newCfg.SleepMs
			s.d.Throttler.UpdateSleep(int64(newCfg.SleepMs))
		}
		if newCfg.MaxLagSec > 0 {
			s.runtimeCfg.MaxLagSec = newCfg.MaxLagSec
		}
		if newCfg.NiceRatio >= 0 {
			s.runtimeCfg.NiceRatio = newCfg.NiceRatio
			s.d.Throttler.UpdateNiceRatio(newCfg.NiceRatio)
		}
		s.mu.Unlock()
		s.jsonResp(w, 200, map[string]interface{}{"status": "ok", "config": s.runtimeCfg})
		return
	}
	if r.Method == http.MethodGet {
		s.mu.RLock()
		defer s.mu.RUnlock()
		s.jsonResp(w, 200, s.runtimeCfg)
		return
	}
	s.jsonResp(w, 405, map[string]string{"error": "method not allowed"})
}
