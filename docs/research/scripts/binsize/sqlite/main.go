//go:build research

// Command sqlite is the SQLite step of the size ladder of #325: the driver memcue uses, one FTS5 query, and nothing of
// memcue, so its size is what the driver costs with the pclntab its functions bring.
package main

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", os.Args[1])
	if err != nil {
		panic(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM fact_fts WHERE fact_fts MATCH ?`, os.Args[2]).Scan(&n); err != nil {
		panic(err)
	}
	fmt.Println(n)
}
