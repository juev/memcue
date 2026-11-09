package main

import (
	"errors"
	"os"
)

// Historical dump/merge have explicit file operands and remain local. These
// implicit-base maintenance workflows require an explicit local invocation.
func remoteCommandAllowed(command string) error {
	if os.Getenv("MEMCUE_POSTGRES_DSN") == "" {
		return nil
	}
	switch command {
	case "migrate", "import", "install", "notes", "dream", "reindex":
		return errors.New("command unavailable for PostgreSQL; explicitly unset MEMCUE_POSTGRES_DSN for local maintenance")
	}
	return nil
}

// migratesToPostgres reports whether the command line is migrate -postgres: the one maintenance command that takes the
// DSN, as the target of a copy of the local base. Its flags are checked again when the command parses them.
func migratesToPostgres(args []string) bool {
	if len(args) < 1 || args[0] != "migrate" {
		return false
	}
	for _, a := range args[1:] {
		switch a {
		case "-postgres", "--postgres", "-postgres=true", "--postgres=true":
			return true
		case "--":
			return false
		}
	}
	return false
}
