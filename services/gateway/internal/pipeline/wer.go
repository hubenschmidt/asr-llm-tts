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

	// Levenshtein DP at word level — two-row optimization.
	// previousRow[j] = edit distance between ref[:i-1] and hyp[:j].
	// currentRow[j]  = edit distance between ref[:i]   and hyp[:j].
	previousRow := make([]int, len(hyp)+1)
	currentRow := make([]int, len(hyp)+1)
	for j := range previousRow {
		previousRow[j] = j
	}

	for i := 1; i <= len(ref); i++ {
		currentRow[0] = i
		for j := 1; j <= len(hyp); j++ {
			cost := 1
			if ref[i-1] == hyp[j-1] {
				cost = 0
			}
			deletionCost := previousRow[j] + 1
			insertionCost := currentRow[j-1] + 1
			substitutionCost := previousRow[j-1] + cost
			currentRow[j] = min3(deletionCost, insertionCost, substitutionCost)
		}
		previousRow, currentRow = currentRow, previousRow
	}

	return float64(previousRow[len(hyp)]) / float64(len(ref))
}

func min3(a, b, c int) int {
	return min(a, min(b, c))
}
