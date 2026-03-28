package main

import (
	"testing"
	"time"
)

func TestChunkDateRange_SingleDay(t *testing.T) {
	d := time.Date(2026, 3, 27, 0, 0, 0, 0, time.UTC)
	chunks := chunkDateRange(d, d, 7)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].start != "2026-03-27" || chunks[0].end != "2026-03-27" {
		t.Errorf("chunk = %+v", chunks[0])
	}
}

func TestChunkDateRange_ExactWeek(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC)
	chunks := chunkDateRange(start, end, 7)
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].start != "2026-03-01" || chunks[0].end != "2026-03-07" {
		t.Errorf("chunk = %+v", chunks[0])
	}
}

func TestChunkDateRange_MultipleChunks(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	chunks := chunkDateRange(start, end, 7)

	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}

	// First chunk: Mar 1-7
	if chunks[0].start != "2026-03-01" || chunks[0].end != "2026-03-07" {
		t.Errorf("chunk[0] = %+v", chunks[0])
	}
	// Second chunk: Mar 8-14
	if chunks[1].start != "2026-03-08" || chunks[1].end != "2026-03-14" {
		t.Errorf("chunk[1] = %+v", chunks[1])
	}
	// Third chunk: Mar 15-20 (capped at end)
	if chunks[2].start != "2026-03-15" || chunks[2].end != "2026-03-20" {
		t.Errorf("chunk[2] = %+v", chunks[2])
	}
}

func TestEnvInt(t *testing.T) {
	// Missing env var — should use fallback
	got := envInt("GROWUD_TEST_NONEXISTENT_12345", 42)
	if got != 42 {
		t.Errorf("got %d, want 42", got)
	}

	// Set env var
	t.Setenv("GROWUD_TEST_INT", "99")
	got = envInt("GROWUD_TEST_INT", 0)
	if got != 99 {
		t.Errorf("got %d, want 99", got)
	}

	// Invalid value — should use fallback
	t.Setenv("GROWUD_TEST_BAD", "abc")
	got = envInt("GROWUD_TEST_BAD", 10)
	if got != 10 {
		t.Errorf("got %d, want 10", got)
	}
}

func TestPlantStatus(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{0, "Offline"},
		{1, "Online"},
		{99, "Unknown (99)"},
	}
	for _, tt := range tests {
		if got := plantStatus(tt.status); got != tt.want {
			t.Errorf("plantStatus(%d) = %q, want %q", tt.status, got, tt.want)
		}
	}
}
