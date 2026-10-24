//go:build research

// Command empty is the floor of the size ladder of #325: the Go runtime and nothing else.
package main

import "os"

func main() { os.Exit(len(os.Args) - 1) }
