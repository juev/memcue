package main

import (
	"io"
)

// The short forms the tests call; the binary always passes the part or the stated room itself.

// runSessionStart is the whole session-start text in one block, part 0.
func runSessionStart(in io.Reader, out io.Writer) error {
	return runSessionStartPart(in, out, 0)
}

// runSessionCard includes repository pitfalls.
func runSessionCard(in io.Reader, out io.Writer) error {
	return runSessionCardMode(in, out, true)
}
