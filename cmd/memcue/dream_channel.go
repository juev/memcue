package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/juev/memcue/internal/store"
)

// owesChannel says whether a dream still has to label the fact by channel (#625): an active global rule or decision
// without one. The card takes from global only the facts labelled behaviour, so a fact waits here until a dream read
// it; a correction of the text drops the label and brings the fact back.
func owesChannel(f store.Fact) bool {
	return f.Channel == "" && (f.Kind == "rule" || f.Kind == "decision") && strings.EqualFold(f.Scope, "global")
}

// renderChannelInput is the JSON the channel task reads: the id, kind, source and text of each global fact.
func renderChannelInput(facts []store.Fact) (string, error) {
	type item struct {
		ID     string `json:"id"`
		Kind   string `json:"kind,omitempty"`
		Source string `json:"source,omitempty"`
		Text   string `json:"text"`
	}
	items := make([]item, len(facts))
	for i, f := range facts {
		items[i] = item{ID: f.ID, Kind: f.Kind, Source: f.Source, Text: f.Content}
	}
	b, err := json.Marshal(map[string]any{"facts": items})
	return string(b), err
}

// applyChannels writes the channels of a checked answer, keeping the creation time. A fact retired since the packet
// or changed in more than its labels is refused: the model judged another text, and the next packet gives the fact
// again, since it still has no label.
func applyChannels(s *store.Store, channels []dreamPlanChannel, saw map[string]int, rec *dreamRecord,
	out io.Writer) error {
	for _, c := range channels {
		cur, err := s.Get(c.ID)
		if errors.Is(err, store.ErrNotFound) {
			rec.Refused = append(rec.Refused, "channel "+c.ID+": "+err.Error())
			continue
		}
		if err != nil {
			return err
		}
		same := cur.Version == c.Version
		if !same && cur.Status == store.Active {
			if same, err = onlyLabelsSince(s, c.ID, c.Version); err != nil {
				return err
			}
		}
		switch {
		case cur.Status != store.Active:
			rec.Refused = append(rec.Refused, "channel "+c.ID+": retired since the packet")
			continue
		case !same:
			rec.Refused = append(rec.Refused, "channel "+c.ID+": changed since the packet")
			continue
		}
		f, err := s.SetChannel(cur.ID, cur.Version, c.Channel)
		if err != nil {
			return err
		}
		if saw[f.ID] == cur.Version {
			saw[f.ID] = f.Version
		}
		rec.Labelled = append(rec.Labelled, f.ID+" "+c.Channel)
		if _, err := fmt.Fprintf(out, "channel %s %s\n", f.ID, c.Channel); err != nil {
			return err
		}
	}
	return nil
}
