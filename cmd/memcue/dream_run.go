package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/juev/memcue/internal/extract"
)

// dreamRunTimeout bounds the endpoint call of one dream: a packet of 40 fresh facts, 50 to expand, 40 to translate and
// 30 groups is about 80,000 characters in and up to 20,000 tokens out.
const dreamRunTimeout = 10 * time.Minute

// dreamRunFn is the endpoint call of dream -run, replaced in tests.
var dreamRunFn = func(packet string) ([]byte, extract.Tokens, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dreamRunTimeout)
	defer cancel()
	return extract.DreamAnswer(ctx, packet)
}

// runDreamOnce is memcue dream -run (#401): the whole dream on a host whose plugin has no session model to hand the
// packet to, such as Hermes, which starts it after an extraction. It asks what the session start of Claude Code asks
// before it says a dream is due: whether MEMCUE_DREAM_EVERY has passed and some fact is fresh or owed a translation.
// With nothing due it makes no call. Otherwise it prepares the packet, sends it to the extraction's endpoint and
// applies the answer under the same checks as -apply; dream.log records the run either way, with its tokens.
func runDreamOnce(out io.Writer) error {
	if extract.API.URL == "" {
		return fmt.Errorf("dream -run: %w", extract.ErrNoEndpoint)
	}
	dir, err := stateDir()
	if err != nil {
		return err
	}
	fresh, owed, last, err := dreamDue(dir, dreamEveryFromEnv(dreamEvery))
	if err != nil {
		return err
	}
	if fresh == 0 && owed == 0 {
		_, err := fmt.Fprintln(out, "dream: nothing due")
		return err
	}
	unlock, err := lockDream(dir)
	if err != nil {
		return err
	}
	defer unlock()
	seen, err := readDreamSeen(dir)
	if err != nil {
		return err
	}
	var packet bytes.Buffer
	rec, err := prepare(dir, last, seen, false, &packet)
	if err != nil || rec.Packet == "" {
		if err != nil {
			rec.Error = err.Error()
		} else if _, err := io.Copy(out, &packet); err != nil {
			return err
		}
		return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	answer, tokens, err := dreamRunFn(packet.String())
	if err != nil {
		rec.Tokens, rec.Error = &tokens, "endpoint: "+err.Error()
		return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
	}
	// The answer stays next to its packet: what the model said is the record a refused action is checked by.
	path := filepath.Join(dir, "dream.answer.json")
	if err := os.WriteFile(path, answer, 0o600); err != nil {
		return err
	}
	rec, err = applyAnswer(dir, path, out)
	rec.Tokens = &tokens
	if err != nil {
		rec.Error = err.Error()
	}
	return errors.Join(err, appendJSON(filepath.Join(dir, "dream.log"), rec))
}
