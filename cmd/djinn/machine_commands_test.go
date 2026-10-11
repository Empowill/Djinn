package main

import (
	"testing"
)

func TestSparkline(t *testing.T) {
	tests := []struct {
		name   string
		values []float64
		want   string
	}{
		{"empty", nil, ""},
		{"all zero", []float64{0, 0, 0}, "   "},
		{"constant", []float64{5, 5, 5}, "   "},
		{"ascending", []float64{1, 2, 3, 4, 5, 6, 7, 8}, " ▂▃▄▅▆▇█"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sparkline(tt.values)
			if got != tt.want {
				t.Errorf("sparkline(%v) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes uint64
		want  string
	}{
		{0, "-"},
		{512, "0 KiB"},
		{1024, "1 KiB"},
		{500 * (1 << 20), "500 MiB"},
		{2 * (1 << 30), "2.0 GiB"},
	}

	for _, tt := range tests {
		if got := formatBytes(tt.bytes); got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
