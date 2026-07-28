package repository

import (
	"testing"
	"time"
)

func TestStartOfLocalDay(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*60*60)
	input := time.Date(2026, 7, 18, 15, 30, 0, 0, loc)

	got := startOfLocalDay(input)
	want := time.Date(2026, 7, 18, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("startOfLocalDay() = %v, want %v", got, want)
	}
}
