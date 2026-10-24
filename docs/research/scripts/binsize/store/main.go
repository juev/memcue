//go:build research

// Command store is the store step of the size ladder of #325: internal/store over the driver, one search.
package main

import (
	"fmt"
	"os"

	"github.com/juev/memcue/internal/store"
)

func main() {
	s, err := store.OpenReadOnly(os.Args[1])
	if err != nil {
		panic(err)
	}
	hits, err := s.Search(os.Args[2:], 80)
	if err != nil {
		panic(err)
	}
	fmt.Println(len(hits))
}
