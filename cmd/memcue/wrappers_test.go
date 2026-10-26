package main

import (
	"io"

	"github.com/juev/memcue/internal/store"
)

// The short forms the tests call; the binary always passes the part, the dream flag or the stated room itself.

// runSessionStart is the whole session-start text in one block, part 0.
func runSessionStart(in io.Reader, out io.Writer) error {
	return runSessionStartPart(in, out, 0)
}

// runSessionCard is the card without the dream hint.
func runSessionCard(in io.Reader, out io.Writer) error {
	return runSessionCardWith(in, out, false)
}

// renderCard is the card with the stated facts inside the budget (#487).
func renderCard(facts []store.Fact, budget int) (string, []string, int, cardFit, error) {
	return renderCardWith(facts, budget, 0)
}

// groupFacts joins each fresh fact with its neighbours; groups that share a fact become one.
func groupFacts(s *store.Store, active, fresh []store.Fact) ([][]store.Fact, error) {
	gr := newGrouper(active)
	for _, f := range fresh {
		if err := gr.add(s, f); err != nil {
			return nil, err
		}
	}
	return gr.groups(), nil
}
