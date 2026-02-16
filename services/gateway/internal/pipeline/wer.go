package pipeline

import "strings"

// ComputeWER calculates Word Error Rate between reference and hypothesis transcripts.
// Returns (substitutions + insertions + deletions) / len(referenceWords).
// Returns 0 if reference is empty.
func ComputeWER(reference, hypothesis string) float64 {
	ref := strings.Fields(strings.ToLower(reference))
	hyp := strings.Fields(strings.ToLower(hypothesis))

	if len(ref) == 0 {
		return 0
	}

	// Levenshtein DP at word level — two-row optimization
	prev := make([]int, len(hyp)+1)
	curr := make([]int, len(hyp)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ref); i++ {
		curr[0] = i
		for j := 1; j <= len(hyp); j++ {
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}

	return float64(prev[len(hyp)]) / float64(len(ref))
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
