package main

import (
	"math"
	"testing"
)

func TestIsRelevant(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		heading  string
		contains []string
		want     bool
	}{
		{name: "match", heading: "go-style/interfaces/ownership", contains: []string{"interfaces"}, want: true},
		{name: "case-insensitive", heading: "Go-Style/Errors", contains: []string{"errors"}, want: true},
		{name: "any of", heading: "go-style/naming", contains: []string{"errors", "naming"}, want: true},
		{name: "no match", heading: "go-style/slices", contains: []string{"interfaces"}, want: false},
		{name: "empty term ignored", heading: "go-style/x", contains: []string{""}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := isRelevant(tt.heading, tt.contains); got != tt.want {
				t.Fatalf("isRelevant = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFirstRelevantRank(t *testing.T) {
	t.Parallel()

	paths := []string{"go/slices", "go/errors/wrapping", "go/interfaces"}
	if got := firstRelevantRank(paths, []string{"interfaces"}); got != 3 {
		t.Fatalf("rank = %d, want 3", got)
	}
	if got := firstRelevantRank(paths, []string{"errors"}); got != 2 {
		t.Fatalf("rank = %d, want 2", got)
	}
	if got := firstRelevantRank(paths, []string{"generics"}); got != 0 {
		t.Fatalf("rank = %d, want 0 (miss)", got)
	}
}

func TestAggregate(t *testing.T) {
	t.Parallel()

	// ranks: hit@1, hit@2, miss, hit@5  (k=10)
	m := aggregate([]int{1, 2, 0, 5}, 10)
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
	if m := aggregate(nil, 10); m.Queries != 0 || m.MRR != 0 {
		t.Fatalf("empty aggregate = %+v", m)
	}
}

func approx(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}
