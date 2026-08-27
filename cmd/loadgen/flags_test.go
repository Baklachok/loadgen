package main

import (
	"testing"
	"time"
)

func TestWarmupFlagParsing(t *testing.T) {
	tests := []struct {
		in           string
		wantRequests int
		wantDuration time.Duration
		wantErr      bool
	}{
		{in: "5s", wantDuration: 5 * time.Second},
		{in: "1m30s", wantDuration: 90 * time.Second},
		{in: "100", wantRequests: 100},
		{in: "0", wantRequests: 0},
		{in: "abc", wantErr: true},
		{in: "", wantErr: true},
		{in: "5 s", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var f warmupFlag
			err := f.Set(tt.in)

			if tt.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) прошёл, ожидалась ошибка", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tt.in, err)
			}
			if f.requests != tt.wantRequests || f.duration != tt.wantDuration {
				t.Errorf("Set(%q) → requests=%d duration=%v, ожидалось %d и %v",
					tt.in, f.requests, f.duration, tt.wantRequests, tt.wantDuration)
			}
		})
	}
}

// Повторный флаг должен вести себя как обычный: побеждает последний,
// а не складывается с предыдущим.
func TestWarmupFlagLastWins(t *testing.T) {
	var f warmupFlag

	if err := f.Set("5s"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("100"); err != nil {
		t.Fatal(err)
	}

	if f.duration != 0 {
		t.Errorf("duration = %v, ожидался 0: форму перетёрли числом", f.duration)
	}
	if f.requests != 100 {
		t.Errorf("requests = %d, ожидалось 100", f.requests)
	}
}
