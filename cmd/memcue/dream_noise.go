package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
	"github.com/juev/memcue/internal/vet"
)

const noiseReviewFile = "dream.noise.json"

const noiseInstructions = "This packet is for noise review only. Propose only retire actions with kind noise and an evidence-based reason. " +
	"For candidates you keep, omit the action. Return an explicit empty actions array if you keep all candidates.\n\n"

func readDreamState(path string, value any) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("%s: expected an object", filepath.Base(path))
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return nil
}

func noiseHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func noisePending(active []store.Fact, reviewed map[string]string, from time.Time) []store.Fact {
	var pending []store.Fact
	for _, f := range active {
		if !f.Created.Before(from) && vet.CaptureNoise(f) != "" && reviewed[f.ID] != noiseHash(f.Content) {
			pending = append(pending, f)
		}
	}
	return pending
}

func (inv *invocation) prepareNoise(dir string, from time.Time, list bool, packets int, out io.Writer) (dreamRecord, dreamWork, error) {
	start := time.Now()
	rec := dreamRecord{Time: start.UTC().Format(time.RFC3339), Since: from.UTC().Format(time.RFC3339)}
	var work dreamWork
	reviewed := map[string]string{}
	if err := readDreamState(filepath.Join(dir, noiseReviewFile), &reviewed); err != nil {
		return rec, work, err
	}
	s, err := inv.openBase()
	if err != nil {
		return rec, work, err
	}
	defer func() { _ = s.Close() }()
	active, err := s.Active()
	if err != nil {
		return rec, work, err
	}
	pending := noisePending(active, reviewed, from)
	remaining := len(pending)
	rec.NoiseRemaining = &remaining
	batch := pending[:min(remaining, packets*min(packetFresh, packetGroups))]
	p := dreamPacket{ID: start.UTC().Format(time.RFC3339Nano), Start: start, NoiseOnly: true, Noise: versions(batch)}
	if !from.IsZero() {
		p.NoiseSince = &from
	}
	var groups [][]store.Fact
	for _, f := range batch {
		groups = append(groups, []store.Fact{f})
		p.Groups = append(p.Groups, map[string]int{f.ID: f.Version})
	}
	rec.Groups, rec.Noise = len(groups), len(batch)
	if list {
		_, err := io.WriteString(out, renderGroups(groups, freshSet(batch), p.Noise))
		return rec, work, err
	}
	if len(batch) == 0 {
		_, err := fmt.Fprintln(out, "memcue: nothing for the dream (0 noise candidates remaining)")
		return rec, work, err
	}
	text, err := renderPacket(p.ID, nil, nil, nil, groups, freshSet(batch), p.Noise)
	if err != nil {
		return rec, work, err
	}
	if err := writeJSONFile(filepath.Join(dir, packetFile), p); err != nil {
		return rec, work, err
	}
	rec.Packet = p.ID
	work = dreamWork{id: p.ID, noiseOnly: true, groups: groups, fresh: freshSet(batch), noise: p.Noise}
	_, err = io.WriteString(out, noiseInstructions+text)
	return rec, work, err
}

func validateNoiseAnswer(answer dreamAnswer) error {
	if answer.Actions == nil || len(answer.Splits)+len(answer.Scopes) != 0 {
		return errors.New("dream -noise: answer must contain an actions array and no splits or scope changes")
	}
	return nil
}

func (inv *invocation) applyNoise(dir string, p dreamPacket, answer dreamAnswer, rec dreamRecord, out io.Writer) (dreamRecord, error) {
	if err := validateNoiseAnswer(answer); err != nil {
		return rec, err
	}
	path := filepath.Join(dir, noiseReviewFile)
	reviewed := map[string]string{}
	if err := readDreamState(path, &reviewed); err != nil {
		return rec, err
	}
	s, err := inv.openBase()
	if err != nil {
		return rec, err
	}
	defer func() { _ = s.Close() }()

	rec.Groups, rec.Noise = len(p.Groups), len(p.Noise)
	var groups [][]store.Fact
	ids := make([]string, 0, len(p.Noise))
	for _, group := range p.Groups {
		for id, v := range group {
			if len(group) != 1 || v <= 0 || p.Noise[id] != v {
				return rec, errors.New("dream -noise: invalid candidate group in packet")
			}
			ids = append(ids, id)
			groups = append(groups, []store.Fact{{ID: id, Version: v}})
		}
	}
	if len(ids) != len(p.Noise) {
		return rec, errors.New("dream -noise: missing candidate group in packet")
	}
	slices.Sort(ids)
	proposed := map[string]int{}
	for _, a := range answer.Actions {
		proposed[a.ID]++
	}
	var eligible []extract.Action
	for _, a := range answer.Actions {
		if proposed[a.ID] > 1 {
			rec.Refused = append(rec.Refused, a.Op+" "+a.ID+": second action on the fact")
			continue
		}
		if a.Op != "retire" || a.Kind != "noise" {
			rec.Refused = append(rec.Refused, a.Op+" "+a.ID+": noise-only packets allow only noise retirement")
			continue
		}
		eligible = append(eligible, a)
	}
	actions, refused := checkActionsWithNoise(groups, eligible, p.Noise)
	rec.Refused = append(rec.Refused, refused...)
	retire := make(map[string]extract.Action, len(actions))
	for _, a := range actions {
		retire[a.ID] = a
	}
	for _, id := range ids {
		if err := inv.ctx.Err(); err != nil {
			return rec, err
		}
		f, err := s.Get(id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return rec, err
		}
		if err != nil || f.Version != p.Noise[id] || f.Status != store.Active || vet.CaptureNoise(f) == "" {
			rec.Refused = append(rec.Refused, id+": candidate changed since the packet")
			continue
		}
		if a, ok := retire[id]; ok {
			inv.effect = true
			if _, err := s.Retire(id, f.Version); err != nil {
				if errors.Is(err, store.ErrChanged) || errors.Is(err, store.ErrRetired) || errors.Is(err, store.ErrNotFound) {
					rec.Refused = append(rec.Refused, id+": candidate changed since the packet")
					continue
				}
				return rec, err
			}
			rec.Actions = append(rec.Actions, a)
			rec.NoiseChecked++
		} else if proposed[id] == 0 {
			reviewed[id] = noiseHash(f.Content)
			rec.NoiseChecked++
		}
	}
	active, err := s.Active()
	if err != nil {
		return rec, err
	}
	for id := range reviewed {
		_, live := slices.BinarySearchFunc(active, id, func(f store.Fact, id string) int {
			return strings.Compare(f.ID, id)
		})
		if !live {
			delete(reviewed, id)
		}
	}
	var from time.Time
	if p.NoiseSince != nil {
		from = *p.NoiseSince
	}
	remaining := len(noisePending(active, reviewed, from))
	rec.NoiseRemaining = &remaining
	rec.Grouped = groupRecords(p, answer.Actions, rec.Actions)
	if err := writeJSONFile(path, reviewed); err != nil {
		return rec, err
	}
	for _, a := range rec.Actions {
		if _, err := fmt.Fprintf(out, "retire %s: %s\n", a.ID, a.Reason); err != nil {
			return rec, err
		}
	}
	for _, refusal := range rec.Refused {
		if _, err := fmt.Fprintf(out, "refused %s\n", refusal); err != nil {
			return rec, err
		}
	}
	_, err = fmt.Fprintf(out, "dream -noise: %d candidates checked, %d retired, %d remaining, %d refused\n",
		rec.NoiseChecked, len(rec.Actions), remaining, len(rec.Refused))
	return rec, err
}
