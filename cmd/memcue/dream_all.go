package main

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"time"
)

type manualDreamSweep struct {
	After      string    `json:"after"`
	Through    string    `json:"through"`
	Completed  int       `json:"completed"`
	FinishedAt time.Time `json:"finished_at"`
}

func (inv *invocation) runDreamAll(dir string, packets int, out io.Writer) error {
	path := filepath.Join(dir, "dream.all.json")
	var sweep manualDreamSweep
	if err := readDreamState(path, &sweep); err != nil {
		return err
	}
	active, fresh, scope, err := inv.serverDreamQueues(dir)
	if err != nil {
		return err
	}
	if sweep.Through == "" && !time.Now().Before(sweep.FinishedAt.Add(serverSweepEvery)) {
		sweep = manualDreamSweep{}
		for _, fact := range slices.Backward(active) {
			if !fresh[fact.ID] {
				sweep.Through = fact.ID
				break
			}
		}
		if sweep.Through == "" {
			sweep.FinishedAt = time.Now()
		}
		if err := writeJSONFile(path, sweep); err != nil {
			return err
		}
	}
	stalls, preferOld := 0, sweep.After != ""
	for pass := 1; ; pass++ {
		if err := inv.ctx.Err(); err != nil {
			return err
		}
		remaining := len(oldDreamFacts(active, sweep.After, sweep.Through, fresh))
		if sweep.Through != "" && remaining == 0 {
			sweep.Through, sweep.FinishedAt = "", time.Now()
			if err := writeJSONFile(path, sweep); err != nil {
				return err
			}
		}
		pending := len(fresh) + scope
		if remaining == 0 && pending == 0 {
			_, err := fmt.Fprintf(out, "dream -all: done after %d passes; remaining 0\n", pass-1)
			return err
		}
		if remaining == 0 && stalls >= dreamAllStalls {
			return fmt.Errorf("dream -all: stopped without progress; %d fresh and scope checks remain", pending)
		}
		if pass > dreamAllPasses {
			return fmt.Errorf("dream -all: stopped after %d passes; %d old and %d fresh or scope checks remain; run it again", dreamAllPasses, remaining, pending)
		}
		if _, err := fmt.Fprintf(out, "dream -all: pass %d\n", pass); err != nil {
			return err
		}
		options := dreamOptions{all: true}
		old := remaining > 0 && (preferOld || pending == 0 || stalls >= dreamAllStalls)
		if old {
			options.sweep = &dreamSweepRange{after: sweep.After, through: sweep.Through}
		}
		rec, _, err := inv.dreamPass(dir, packets, options, out)
		if err != nil {
			return fmt.Errorf("dream -all: pass %d: %w", pass, err)
		}
		preferOld = !old
		if old && rec.Swept != 0 {
			sweep.After = rec.sweepCursor
			sweep.Completed += rec.Swept
			if err := writeJSONFile(path, sweep); err != nil {
				return err
			}
		}
		active, fresh, scope, err = inv.serverDreamQueues(dir)
		if err != nil {
			return err
		}
		if old {
			remaining = len(oldDreamFacts(active, sweep.After, sweep.Through, fresh))
			if _, err := fmt.Fprintf(out, "dream -all: swept %d of %d, remaining %d\n", sweep.Completed, sweep.Completed+remaining, remaining); err != nil {
				return err
			}
			if rec.Swept == 0 && remaining != 0 {
				return fmt.Errorf("dream -all: stopped without progress; %d old facts remain", remaining)
			}
		} else if len(fresh)+scope < pending || len(rec.Splits)+len(rec.Actions) != 0 || rec.scopeAdvanced {
			stalls = 0
		} else {
			stalls++
		}
	}
}
