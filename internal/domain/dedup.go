package domain

import (
	"strings"
	"unicode"
)

const shingleSize = 3

// DedupeChunks drops chunks whose content is a near-duplicate of an earlier kept
// chunk, measured by word-shingle Jaccard similarity at or above threshold.
// Order is preserved. A threshold <= 0 disables dedup and returns the input.
func DedupeChunks(chunks []Chunk, threshold float64) []Chunk {
	if threshold <= 0 || len(chunks) == 0 {
		return chunks
	}

	kept := make([]Chunk, 0, len(chunks))
	keptShingles := make([]map[string]struct{}, 0, len(chunks))
	for _, c := range chunks {
		shingles := shingleSet(c.content)
		duplicate := false
		for _, prev := range keptShingles {
			if jaccard(shingles, prev) >= threshold {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		kept = append(kept, c)
		keptShingles = append(keptShingles, shingles)
	}
	return kept
}

// shingleSet returns the set of overlapping word n-grams of a text (normalized to
// lowercase words). Texts shorter than shingleSize fall back to a single shingle
// of all their words, so they still compare exactly.
func shingleSet(text string) map[string]struct{} {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	set := make(map[string]struct{})
	if len(words) < shingleSize {
		if len(words) > 0 {
			set[strings.Join(words, " ")] = struct{}{}
		}
		return set
	}
	for i := 0; i+shingleSize <= len(words); i++ {
		set[strings.Join(words[i:i+shingleSize], " ")] = struct{}{}
	}
	return set
}

func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for s := range a {
		if _, ok := b[s]; ok {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}
