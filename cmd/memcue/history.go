package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/juev/memcue/internal/store"
)

// runHistory reads immutable versions without treating a historical file operand
// as a backend selector. dump remains the explicit local-file operation.
func (inv *invocation) runHistory(args []string, out io.Writer) error {
	out = inv.writer(out)
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print complete fact versions as JSON")
	ids, err := parseAnywhere(fs, args)
	if err != nil {
		return err
	}
	if len(ids) != 1 {
		return fmt.Errorf("usage: memcue history [-json] ID")
	}
	return inv.withStore(func(s *store.Store) error {
		versions, err := s.History(ids[0])
		if err != nil {
			return err
		}
		if len(versions) == 0 {
			return store.ErrNotFound
		}
		if *asJSON {
			return inv.encoder(out).Encode(versions)
		}
		for _, f := range versions {
			if _, err := fmt.Fprintf(out, "%s v%d %s\n%s\n", f.ID, f.Version, f.Status, f.Content); err != nil {
				return err
			}
			if f.Status == store.Retired {
				reason := f.RetirementReason
				if reason == "" {
					reason = "unavailable"
				}
				if _, err := fmt.Fprintf(out, "Retirement reason: %s\n", reason); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
