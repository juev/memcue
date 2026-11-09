package main

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"

	"github.com/juev/memcue/internal/store"
)

// runMigratePostgres copies the latest version of every fact of the local base into an empty PostgreSQL base, which
// keeps no history (#700): memcue migrate -postgres [-apply]. Without -apply it reports what it would copy and where, and writes nothing. The source is the
// local base (MEMCUE_FACTS), the target MEMCUE_POSTGRES_DSN; after the copy the DSN selects PostgreSQL as the authority.
func (inv *invocation) runMigratePostgres(apply bool, out io.Writer) error {
	dsn := os.Getenv("MEMCUE_POSTGRES_DSN")
	if dsn == "" {
		return errors.New("migrate -postgres: set MEMCUE_POSTGRES_DSN to the target base")
	}
	target, err := postgresTarget(dsn)
	if err != nil {
		return err
	}
	// Opening a missing text history would write an empty one, and the copy would then succeed with no facts.
	source := factsBase().path
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("migrate -postgres: no local fact base at %s", source)
	}
	s, err := inv.openLocalBase()
	if err != nil {
		return err
	}
	rows, err := s.Rows()
	if cerr := s.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("migrate -postgres: the local base at %s holds no facts", source)
	}
	facts := map[string]bool{}
	for _, f := range rows {
		facts[f.ID] = true
	}
	if _, err := fmt.Fprintf(out, "source %s: %d facts, %d versions\n", source, len(facts), len(rows)); err != nil {
		return err
	}
	state, err := store.InspectPostgres(inv.ctx, dsn)
	if err != nil {
		return err
	}
	switch {
	case !state.Schema:
		_, err = fmt.Fprintf(out, "target %s: no memcue schema yet\n", target)
	case state.Version == 1:
		_, err = fmt.Fprintf(out, "target %s: PostgreSQL schema 1, %d versions\n", target, state.Rows)
	default:
		_, err = fmt.Fprintf(out, "target %s: %d facts\n", target, state.Rows)
	}
	if err != nil {
		return err
	}
	if state.Version == 1 {
		return errors.New("migrate -postgres: the target holds PostgreSQL schema 1, which this memcue does not read; drop the memcue schema with the owner role (DROP SCHEMA memcue CASCADE) and run again")
	}
	if state.Rows != 0 {
		return store.ErrTargetNotEmpty
	}
	if !apply {
		_, err = fmt.Fprintln(out, "nothing written; run again with -apply to copy")
		return err
	}
	if err := store.CopyToPostgres(inv.ctx, dsn, rows); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "copied the latest versions of %d facts and read them back unchanged\n", len(facts))
	return err
}

// postgresTarget names the target base for the report without its password or parameters.
func postgresTarget(dsn string) (string, error) {
	if _, err := store.PostgresConfig(dsn); err != nil {
		return "", err
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", err
	}
	u.User = url.User(u.User.Username())
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}
