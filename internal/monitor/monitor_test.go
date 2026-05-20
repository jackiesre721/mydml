package monitor

import (
	"testing"
)

func TestParseLoadConfig(t *testing.T) {
	tests := []struct {
		cfg  string
		want map[string]int64
	}{
		{
			"Threads_running=25",
			map[string]int64{"Threads_running": 25},
		},
		{
			"Threads_running=25,Connections=1000",
			map[string]int64{"Threads_running": 25, "Connections": 1000},
		},
		{
			"Threads_running = 25 , Connections = 1000",
			map[string]int64{"Threads_running": 25, "Connections": 1000},
		},
		{
			"",
			map[string]int64{},
		},
		{
			"invalid",
			map[string]int64{},
		},
		{
			"key=abc",
			map[string]int64{"key": 0},
		},
	}
	for _, tt := range tests {
		got := parseLoadConfig(tt.cfg)
		if len(got) != len(tt.want) {
			t.Errorf("parseLoadConfig(%q) returned %d entries, want %d", tt.cfg, len(got), len(tt.want))
			continue
		}
		for k, v := range tt.want {
			if got[k] != v {
				t.Errorf("parseLoadConfig(%q)[%q] = %d, want %d", tt.cfg, k, got[k], v)
			}
		}
	}
}

func TestParseLoadConfig_Single(t *testing.T) {
	result := parseLoadConfig("Threads_running=50")
	if result["Threads_running"] != 50 {
		t.Errorf("expected Threads_running=50, got %d", result["Threads_running"])
	}
}
