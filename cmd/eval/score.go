package main

import "strings"

// isRelevant reports whether a result's heading path matches any of the expected
// substrings (case-insensitive).
func isRelevant(headingPath string, contains []string) bool {
	hp := strings.ToLower(headingPath)
	for _, want := range contains {
		if want != "" && strings.Contains(hp, strings.ToLower(want)) {
			return true
		}
	}
	return false
}

// firstRelevantRank returns the 1-based rank of the first relevant result, or 0 if none.
func firstRelevantRank(headingPaths []string, contains []string) int {
	for i, hp := range headingPaths {
		if isRelevant(hp, contains) {
			return i + 1
		}
	}
	return 0
}

type Metrics struct {
	Queries int
	HitAt1  float64
	HitAt3  float64
	HitAtK  float64
	MRR     float64
}

// aggregate turns per-query first-relevant ranks (0 = miss) into retrieval metrics.
func aggregate(ranks []int, k int) Metrics {
	m := Metrics{Queries: len(ranks)}
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
	m.HitAt1 = hit1 / n
	m.HitAt3 = hit3 / n
	m.HitAtK = hitK / n
	m.MRR = sumRR / n
	return m
}
