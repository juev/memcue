package recall

// testParams are the gates the prompt hook used before #361 (the dev set of 2026-10-19), kept as a fixed setting the
// gate tests are written against.
var testParams = Params{
	Candidates:     80,
	MinTerms:       2,
	MinMatched:     3,
	MinCoverage:    0.15,
	MaxPromptTerms: 24,
	MaxFacts:       2,
}

// ids lists the ids of hits, for failure messages.
func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}
