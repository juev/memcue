package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

const (
	flashNoiseBatch = 15
	flashGroupBatch = 6
)

type flashPass struct {
	ctx   context.Context
	reply extract.DreamReply
	calls []dreamCallRecord
}

func (f *flashPass) judge(rec dreamCallRecord, system, input string, cap int, keys []string, allowed ...string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(f.ctx, dreamRunTimeout)
	defer cancel()
	parse := parseFlashLabels
	if rec.Task == "flash-noise" {
		parse = parseFlashNoiseLabels
	}
	var err error
	for range 2 {
		start := time.Now()
		r, requestErr := extract.DreamFlashJudgment(ctx, system, input, cap)
		f.reply.Tokens.Add(r.Tokens)
		rec.Tokens, rec.Model, rec.Error = r.Tokens, ms(time.Since(start)), ""
		var labels map[string]string
		err = requestErr
		if err == nil {
			labels, err = parse(r.Answer, keys, allowed...)
		}
		if err != nil {
			err = fmt.Errorf("%s: %w", rec.Task, err)
			rec.Error = err.Error()
		}
		f.calls = append(f.calls, rec)
		if requestErr != nil || err == nil {
			return labels, err
		}
	}
	if rec.Task == "flash-noise" {
		return nil, err
	}
	// A malformed pair or confirmation cannot authorize a retirement.
	return nil, nil
}

func runFlashDream(ctx context.Context, w dreamWork, packet *dreamPacket) (extract.DreamReply, []dreamCallRecord, error) {
	pass := flashPass{ctx: ctx}
	err := pass.run(w, packet)
	return pass.reply, pass.calls, err
}

func (f *flashPass) run(w dreamWork, packet *dreamPacket) error {
	candidates := make(map[string]store.Fact)
	for _, fact := range w.flashScreen {
		candidates[fact.ID] = fact
	}
	for _, group := range packet.Deferred {
		for _, id := range group.Facts {
			delete(candidates, id)
		}
	}
	facts := maps.Clone(candidates)
	for _, group := range w.groups {
		for _, fact := range group {
			facts[fact.ID] = fact
		}
	}
	ids := slices.Sorted(maps.Keys(facts))
	aliases := make(map[string]string, len(ids))
	for index, id := range ids {
		aliases[id] = fmt.Sprintf("f%02d", index+1)
	}
	aliased := func(facts []store.Fact) []store.Fact {
		out := slices.Clone(facts)
		for i, fact := range out {
			out[i].ID = aliases[fact.ID]
		}
		return out
	}
	noise := make(map[string]int)
	actions := []extract.Action{}
	for chunk := range slices.Chunk(slices.Sorted(maps.Keys(candidates)), flashNoiseBatch) {
		batch := make([]store.Fact, 0, len(chunk))
		keys := make([]string, 0, len(chunk))
		for _, id := range chunk {
			batch = append(batch, facts[id])
			keys = append(keys, aliases[id])
		}
		system, input, err := flashNoisePrompt(aliased(batch))
		if err != nil {
			return err
		}
		labels, err := f.judge(dreamCallRecord{Task: "flash-noise", Facts: len(batch)},
			system, input, 512, keys, "noise", "useful", "uncertain")
		if err != nil {
			return err
		}
		for _, fact := range batch {
			if labels[aliases[fact.ID]] == "noise" {
				noise[fact.ID] = fact.Version
				actions = append(actions, extract.Action{Op: "retire", ID: fact.ID, Kind: "noise",
					Reason: "Flash classified the record as one-off noise without reusable knowledge."})
			}
		}
	}

	isNoise := func(fact store.Fact) bool {
		_, screened := noise[fact.ID]
		return screened
	}
	grouped := make(map[string]bool)
	var comparable, fallback [][]store.Fact
	for _, original := range w.groups {
		for _, fact := range original {
			grouped[fact.ID] = true
		}
		group := slices.DeleteFunc(slices.Clone(original), isNoise)
		if len(group) < 2 {
			continue
		}
		scope := group[0].Scope
		if !strings.HasPrefix(scope, "repo:") || slices.ContainsFunc(group, func(fact store.Fact) bool {
			return fact.Scope != scope
		}) {
			fallback = append(fallback, group)
			continue
		}
		comparable = append(comparable, group)
	}
	var judgePairs func([][]store.Fact) error
	judgePairs = func(groups [][]store.Fact) error {
		promptGroups := make([][]store.Fact, 0, len(groups))
		for _, group := range groups {
			promptGroups = append(promptGroups, aliased(group))
		}
		system, input, err := flashPairPrompt(promptGroups)
		if err != nil {
			return err
		}
		keys := flashPairKeys(promptGroups)
		// A six-record group asks more questions than a two-record group.
		labels, err := f.judge(dreamCallRecord{Task: "flash-pairs", Groups: len(groups)},
			system, input, max(1200, 32*len(keys)), keys, "yes", "no", "uncertain")
		if err != nil {
			return err
		}
		if labels == nil && len(groups) > 1 {
			mid := len(groups) / 2
			if err := judgePairs(groups[:mid]); err != nil {
				return err
			}
			return judgePairs(groups[mid:])
		}
		for _, group := range groups {
			proposed, escalate := flashPairCandidates(group, aliases, labels)
			if escalate {
				fallback = append(fallback, group)
			}
			for _, action := range proposed {
				system, input, err := flashConfirmationPrompt(action, facts)
				if err != nil {
					return err
				}
				verdict, err := f.judge(dreamCallRecord{Task: "flash-confirm", Facts: 2},
					system, input, 64, []string{"verdict"}, "confirm", "reject", "uncertain")
				if err != nil {
					return err
				}
				if verdict["verdict"] == "confirm" {
					actions = append(actions, action)
				}
			}
		}
		return nil
	}
	for groups := range slices.Chunk(comparable, flashGroupBatch) {
		if err := judgePairs(groups); err != nil {
			return err
		}
	}

	w.groups, w.noise = fallback, nil
	w.split = slices.DeleteFunc(slices.Clone(w.split), isNoise)
	w.scope = slices.DeleteFunc(slices.Clone(w.scope), isNoise)
	answer, err := f.reason(w)
	if err != nil {
		return err
	}
	answer.Actions = append(actions, answer.Actions...)
	f.reply.Answer, err = json.Marshal(answer)
	if w.noiseOnly {
		return err
	}
	if err != nil {
		return err
	}
	packet.Noise = noise
	for _, id := range ids {
		if v, ok := noise[id]; ok && !grouped[id] {
			packet.Groups = append(packet.Groups, map[string]int{id: v})
		}
	}
	return nil
}

func (f *flashPass) reason(w dreamWork) (dreamAnswer, error) {
	calls, err := w.calls()
	if err != nil || len(calls) == 0 {
		return dreamAnswer{Packet: w.id}, err
	}
	r, byCall, err := askAll(f.ctx, calls)
	f.calls = append(f.calls, byCall...)
	f.reply.Tokens.Add(r.Tokens)
	f.reply.CostUSD += r.CostUSD
	f.reply.API, f.reply.TTFT = r.API, r.TTFT
	if err != nil {
		return dreamAnswer{}, err
	}
	answer, err := decodeDreamAnswer(r.Answer)
	if err != nil {
		return dreamAnswer{}, err
	}
	if answer.Packet != w.id {
		return dreamAnswer{}, fmt.Errorf("reasoning answer is for packet %q, expected %q", answer.Packet, w.id)
	}
	byID := make(map[string]int)
	for _, group := range w.groups {
		maps.Copy(byID, versions(group))
	}
	for _, action := range answer.Actions {
		_, inGroup := byID[action.ID]
		_, byInGroup := byID[action.By]
		if !inGroup || action.By != "" && !byInGroup || action.Kind == "noise" {
			return dreamAnswer{}, fmt.Errorf("reasoning action for %q is outside its task", action.ID)
		}
	}
	split, scoped := versions(w.split), versions(w.scope)
	for _, item := range answer.Splits {
		if _, ok := split[item.ID]; !ok {
			return dreamAnswer{}, fmt.Errorf("reasoning split for %q is outside its task", item.ID)
		}
	}
	for _, item := range answer.Scopes {
		if _, ok := scoped[item.ID]; !ok {
			return dreamAnswer{}, fmt.Errorf("reasoning scope for %q is outside its task", item.ID)
		}
	}
	return answer, nil
}
