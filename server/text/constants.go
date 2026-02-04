package text

const (
	// SimilarityThreshold is the minimum similarity score for considering
	// two lines as corresponding (modification vs addition/deletion).
	// Below this threshold, lines are treated as unrelated.
	SimilarityThreshold = 0.3

	// ExpectedPositionSimilarityThreshold is a higher threshold used when
	// matching at the expected position during streaming. This prevents
	// false matches to barely-similar lines when a better match exists elsewhere.
	// Must be >= SimilarityThreshold.
	ExpectedPositionSimilarityThreshold = 0.35
)
