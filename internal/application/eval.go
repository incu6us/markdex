package application

import "strings"

const DefaultEvalTopK = 10

// EvalMetrics summarizes retrieval quality over a golden query set.
type EvalMetrics struct {
	Queries int
	MRR     float64
	HitAt1  float64
	HitAt3  float64
	HitAtK  float64
}

// FirstRelevantRank returns the 1-based rank of the first heading path that matches any of
// the expected substrings (case-insensitive), or 0 if none match.
func FirstRelevantRank(headingPaths, relevantContains []string) int {
	for i, hp := range headingPaths {
		if headingMatches(hp, relevantContains) {
			return i + 1
		}
	}
	return 0
}

func headingMatches(headingPath string, contains []string) bool {
	hp := strings.ToLower(headingPath)
	for _, want := range contains {
		if want != "" && strings.Contains(hp, strings.ToLower(want)) {
			return true
		}
	}
	return false
}

// Aggregate turns per-query first-relevant ranks (0 = miss) into retrieval metrics.
func Aggregate(ranks []int, k int) EvalMetrics {
	m := EvalMetrics{Queries: len(ranks)}
	if len(ranks) == 0 {
		return m
	}

	var hit1, hit3, hitK, sumRR float64
	for _, rank := range ranks {
		if rank == 0 {
			continue
		}
		sumRR += 1.0 / float64(rank)
		if rank == 1 {
			hit1++
		}
		if rank <= 3 {
			hit3++
		}
		if rank <= k {
			hitK++
		}
	}

	n := float64(len(ranks))
	m.MRR = sumRR / n
	m.HitAt1 = hit1 / n
	m.HitAt3 = hit3 / n
	m.HitAtK = hitK / n
	return m
}
