package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/juev/memcue/internal/extract"
)

func TestAutomaticDreamRejectsRawObsoleteReplyAtomically(t *testing.T) {
	for _, kind := range []string{"keywords", "queries", "translations"} {
		t.Run(kind, func(t *testing.T) {
			dir := env(t)
			withAPI(t, "https://fixture.invalid")
			_, a, _, _ := dreamBase(t)
			addFactForTest(t, "-kind", "rule", "-scope", "global", "Observatory policies apply everywhere.")
			dreamedHoursAgo(t, dir)
			s, err := openBase()
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Rows()
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			previous := dreamRunFn
			t.Cleanup(func() { dreamRunFn = previous })
			var calls atomic.Int32
			dreamRunFn = func(ctx context.Context, packet string) (extract.DreamReply, error) {
				calls.Add(1)
				state, err := stateDir()
				if err != nil {
					return extract.DreamReply{}, err
				}
				data, err := os.ReadFile(filepath.Join(state, packetFile))
				if err != nil {
					return extract.DreamReply{}, err
				}
				var p dreamPacket
				if err := json.Unmarshal(data, &p); err != nil {
					return extract.DreamReply{}, err
				}
				action := ""
				top := ""
				if hasFacts(packetBlock(packet, "## Данные: область")) {
					if kind == "translations" {
						top = fmt.Sprintf(`,"translations":[{"id":%q,"translation":"obsolete translation"}]`, a)
					}
				} else {
					extra := ""
					if kind == "keywords" {
						extra = `,"keywords":"obsolete"`
					}
					if kind == "queries" {
						extra = `,"queries":["obsolete"]`
					}
					action = fmt.Sprintf(`{"op":"correct","id":%q,"content":"Corrected nightly-sync workflow observation.","reason":"fixture"%s}`, a, extra)
				}
				return extract.DreamReply{Answer: []byte(fmt.Sprintf(`{"packet":%q,"splits":[],"actions":[%s],"scopes":[]%s}`, p.ID, action, top))}, nil
			}
			err = runDream([]string{"-run", "-j", "2"}, &bytes.Buffer{})
			if err == nil {
				t.Error("automatic path normalized away obsolete raw reply fields")
			}
			if calls.Load() != 2 {
				t.Fatalf("want real two-call path, got %d", calls.Load())
			}
			s, err = openBase()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			after, err := s.Rows()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Error("rejected raw reply partially applied correction work")
			}
		})
	}
}
