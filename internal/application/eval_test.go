package application_test

import (
	"math"
	"testing"

	"github.com/incu6us/markdex/internal/application"
)

func TestFirstRelevantRank(t *testing.T) {
	t.Parallel()

	paths := []string{"go/slices", "go/errors/wrapping", "go/interfaces"}

	tests := []struct {
		name     string
		contains []string
		want     int
	}{
		{name: "third", contains: []string{"interfaces"}, want: 3},
		{name: "second", contains: []string{"errors"}, want: 2},
		{name: "case-insensitive", contains: []string{"INTERFACES"}, want: 3},
		{name: "any of", contains: []string{"generics", "slices"}, want: 1},
		{name: "miss", contains: []string{"generics"}, want: 0},
		{name: "empty term ignored", contains: []string{""}, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := application.FirstRelevantRank(paths, tt.contains); got != tt.want {
				t.Fatalf("rank = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAggregate(t *testing.T) {
	t.Parallel()

	// ranks: hit@1, hit@2, miss, hit@5  (k=10)
	m := application.Aggregate([]int{1, 2, 0, 5}, 10)
	if m.Queries != 4 {
		t.Fatalf("queries = %d", m.Queries)
	}
	if !approx(m.HitAt1, 1.0/4) {
		t.Fatalf("hit@1 = %v, want 0.25", m.HitAt1)
	}
	if !approx(m.HitAt3, 2.0/4) {
		t.Fatalf("hit@3 = %v, want 0.5", m.HitAt3)
	}
	if !approx(m.HitAtK, 3.0/4) {
		t.Fatalf("hit@k = %v, want 0.75", m.HitAtK)
	}
	// MRR = (1/1 + 1/2 + 0 + 1/5) / 4 = 1.7 / 4 = 0.425
	if !approx(m.MRR, 1.7/4) {
		t.Fatalf("MRR = %v, want 0.425", m.MRR)
	}
}

func TestAggregateEmpty(t *testing.T) {
	t.Parallel()
	if m := application.Aggregate(nil, 10); m.Queries != 0 || m.MRR != 0 {
		t.Fatalf("empty aggregate = %+v", m)
	}
}

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
