package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/juev/memcue/internal/extract"
	"github.com/juev/memcue/internal/store"
)

// labelAll answers every fact a packet gives to label with behaviour, the way translateAll answers the translations.
func labelAll(t *testing.T) {
	t.Helper()
	old := channelFn
	channelFn = func(in string) ([]extract.Channel, error) {
		var req struct {
			Facts []struct{ ID string } `json:"facts"`
		}
		if err := json.Unmarshal([]byte(in), &req); err != nil {
			return nil, err
		}
		var out []extract.Channel
		for _, f := range req.Facts {
			out = append(out, extract.Channel{ID: f.ID, Channel: store.Behaviour})
		}
		return out, nil
	}
	t.Cleanup(func() { channelFn = old })
}

// channelBase stores a global rule of the agent's, a global decision of the user's, a global pitfall and a rule of a
// repository: the dream labels the first two.
func channelBase(t *testing.T) (rule, decision, pitfall, repo store.Fact) {
	t.Helper()
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	add := func(content, kind, scope, source string) store.Fact {
		f, err := s.AddWith(content, []string{"k"}, "example.com/me/shop 2026-10-19", "",
			store.Meta{Kind: kind, Scope: scope, Source: source})
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * time.Millisecond)
		return f
	}
	rule = add("Коммиты пишутся по-английски.", "rule", "global", "agent")
	decision = add("Прокси слушает порт 8080.", "decision", "global", "user")
	pitfall = add("gh api не понимает флаг -R.", "pitfall", "global", "tool")
	repo = add("В shop релиз собирает make release.", "rule", "repo:example.com/me/shop", "user")
	return rule, decision, pitfall, repo
}

// The dream labels the global rules and decisions without a channel, keeping their creation time (#625): a label
// that is not a channel, a fact the packet did not give and a second answer about one fact are refused. A labelled
// fact is not offered again; the card then carries the agent's rule labelled behaviour and not the user's decision
// labelled knowledge.
func TestDreamLabelsGlobalRulesByChannel(t *testing.T) {
	env(t)
	rule, decision, pitfall, repo := channelBase(t)
	var sent []string
	channelFn = func(in string) ([]extract.Channel, error) {
		sent = append(sent, in)
		return []extract.Channel{
			{ID: rule.ID, Channel: "Behaviour "},
			{ID: decision.ID, Channel: "card"},
			{ID: decision.ID, Channel: "knowledge"},
			{ID: decision.ID, Channel: "outdated"},
			{ID: pitfall.ID, Channel: "command"},
			{ID: repo.ID, Channel: "repository"},
		}, nil
	}
	t.Cleanup(func() { channelFn = func(string) ([]extract.Channel, error) { return nil, nil } })
	var out bytes.Buffer
	if err := dreamModel([]string{"-since", "2099-01-01"}, &out); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], rule.ID) || !strings.Contains(sent[0], decision.ID) ||
		strings.Contains(sent[0], pitfall.ID) || strings.Contains(sent[0], repo.ID) ||
		!strings.Contains(sent[0], `"kind":"decision","source":"user"`) {
		t.Fatalf("channel input %q", sent)
	}
	if f := storedFact(t, rule.ID); f.Channel != store.Behaviour || !f.Created.Equal(rule.Created) || f.Content != rule.Content {
		t.Errorf("the agent's rule %+v, want it labelled behaviour with its creation time", f)
	}
	if f := storedFact(t, decision.ID); f.Channel != "knowledge" {
		t.Errorf("the user's decision %+v, want it labelled knowledge", f)
	}
	for _, want := range []string{
		"channel " + rule.ID + " behaviour",
		"refused channel " + decision.ID + ": card is not a channel",
		"channel " + decision.ID + " knowledge",
		"refused channel " + decision.ID + ": second answer about the fact",
		"refused channel " + pitfall.ID + ": not a fact of the packet to label",
		"refused channel " + repo.ID + ": not a fact of the packet to label",
		"2 labelled, 4 refused",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("out lacks %q:\n%s", want, out.String())
		}
	}
	// The next dream gives only a global rule or decision stored since.
	s, _ := store.Open(factsPath())
	later, err := s.AddWith("Ветки создаются только в worktree.", []string{"k"}, "", "",
		store.Meta{Kind: "rule", Scope: "global"})
	_ = s.Close()
	if err != nil {
		t.Fatal(err)
	}
	sent = nil
	if err := dreamModel([]string{"-since", "2099-01-01"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], later.ID) || strings.Contains(sent[0], rule.ID) ||
		strings.Contains(sent[0], decision.ID) {
		t.Errorf("second dream gave %q, want only %s", sent, later.ID)
	}
}

// A fact corrected between the packet and the answer is refused: the model judged the old text, and the fact, still
// without a label, is given again by the next packet. A version that only moved the fact to a project or gave it
// search queries is not a change, and the label is written on top of it.
func TestDreamRefusesTheChannelOfAChangedFact(t *testing.T) {
	env(t)
	rule, decision, _, _ := channelBase(t)
	channelFn = func(string) ([]extract.Channel, error) {
		return []extract.Channel{{ID: rule.ID, Channel: store.Behaviour}, {ID: decision.ID, Channel: "knowledge"}}, nil
	}
	t.Cleanup(func() { channelFn = func(string) ([]extract.Channel, error) { return nil, nil } })
	path, err := prepareAnswer([]string{"-since", "2099-01-01"}, io.Discard)
	if err != nil || path == "" {
		t.Fatal(path, err)
	}
	s, _ := store.Open(factsPath())
	if _, err := s.Relabel(rule.ID, rule.Version, rule.Kind, "repo:example.com/me/shop", rule.Expires); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Correct(decision.ID, "Прокси слушает порт 8081.", []string{"k"}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	var out bytes.Buffer
	if err := runDream([]string{"-apply", path}, &out); err != nil {
		t.Fatal(err)
	}
	if f := storedFact(t, rule.ID); f.Channel != store.Behaviour || f.Scope != "repo:example.com/me/shop" {
		t.Errorf("a fact that only moved was not labelled: %+v\n%s", f, out.String())
	}
	if f := storedFact(t, decision.ID); f.Channel != "" ||
		!strings.Contains(out.String(), "refused channel "+decision.ID+": changed since the packet") {
		t.Errorf("a corrected fact %+v\n%s", f, out.String())
	}
	var sent []string
	channelFn = func(in string) ([]extract.Channel, error) { sent = append(sent, in); return nil, nil }
	if err := dreamModel([]string{"-since", "2099-01-01"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], decision.ID) || strings.Contains(sent[0], rule.ID) {
		t.Errorf("the next packet gave %q, want the corrected fact %s again", sent, decision.ID)
	}
}

// A fresh fact, or one in a group of the packet, waits for a later packet, as a translation does: a split or a
// correction of the same packet would change it under the label. Once seen, it is given.
func TestDreamChannelWaitsForAFactOfTheSamePacket(t *testing.T) {
	env(t)
	rule, _, _, _ := channelBase(t)
	var sent []string
	channelFn = func(in string) ([]extract.Channel, error) { sent = append(sent, in); return nil, nil }
	t.Cleanup(func() { channelFn = func(string) ([]extract.Channel, error) { return nil, nil } })
	if err := dreamModel([]string{"-since", "2000-01-01"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(sent, " "), rule.ID) {
		t.Errorf("a fresh fact was given to label: %q", sent)
	}
	sent = nil
	if err := dreamModel(nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(sent, " "), rule.ID) {
		t.Errorf("a fact seen by the last dream was not given: %q", sent)
	}
}

// dream -run -all labels every global rule and decision, a call of its own for them, and the loop goes on while
// unlabelled facts wait: a pass that labels facts is not a stall (#625).
func TestDreamRunAllLabelsEveryGlobalFact(t *testing.T) {
	env(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 5 {
		f, err := s.AddWith(fmt.Sprintf("Общее правило номер %d для любой работы.", i), []string{"k"}, "", "",
			store.Meta{Kind: "rule", Scope: "global"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
		time.Sleep(3 * time.Millisecond)
	}
	_ = s.Close()
	old := packetChannel
	packetChannel = 2
	t.Cleanup(func() { packetChannel = old })
	fakeExpand(t, nil)
	fakeDream(t, func(string) []extract.Action { return nil })
	translateAll(t)
	labelAll(t)
	var given []string
	inner := channelFn
	channelFn = func(in string) ([]extract.Channel, error) { given = append(given, in); return inner(in) }
	fakeDreamRun(t, nil)
	var out bytes.Buffer
	if err := runDream([]string{"-run", "-all", "-j", "1"}, &out); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if f := storedFact(t, id); f.Channel != store.Behaviour {
			t.Errorf("fact %s %+v, want it labelled", id, f)
		}
	}
	if len(given) < 3 {
		t.Errorf("%d channel calls for 5 facts at 2 a call:\n%s", len(given), out.String())
	}
}

// A base whose only debt is global rules and decisions without a channel is due once the interval has passed, and
// the hint names them (#634): the card leaves such facts out until a dream labels them, so a quiet machine must not
// wait for a fresh fact. Once they are labelled, nothing is due.
func TestDreamDueForGlobalFactsWithoutAChannel(t *testing.T) {
	dir := env(t)
	rule, decision, pitfall, repo := channelBase(t)
	s, err := store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, f := range []store.Fact{rule, decision, pitfall, repo} {
		// a translation each, so that the channel is the only debt
		f, err := s.SetQueries(f.ID, f.Version, []string{extract.TranslationPrefix + "x"})
		if err != nil {
			t.Fatal(err)
		}
		seen[f.ID] = f.Version
	}
	_ = s.Close()
	state := filepath.Join(dir, "state", "memcue")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeDreamSeen(state, seen); err != nil {
		t.Fatal(err)
	}
	if err := markDream(state, time.Now().Add(-59*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, owed, _, err := dreamDue(state, time.Hour); err != nil || !owed.none() {
		t.Fatalf("due within the hour: %+v %v", owed, err)
	}
	if err := markDream(state, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	fresh, owed, _, err := dreamDue(state, time.Hour)
	if err != nil || fresh != 0 || owed != (dreamOwed{channel: 2}) {
		t.Fatalf("due: fresh %d owed %+v %v, want only the rule and the decision owed a channel", fresh, owed, err)
	}
	if hint := dreamHint(); !strings.Contains(hint, "2 global rules and decisions lack a channel") ||
		strings.Contains(hint, "wait for the dream") || strings.Contains(hint, "translation") {
		t.Errorf("hint %q", hint)
	}
	s, err = store.Open(factsPath())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{rule.ID, decision.ID} {
		f, err := s.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		f, err = s.SetChannel(f.ID, f.Version, store.Behaviour)
		if err != nil {
			t.Fatal(err)
		}
		seen[f.ID] = f.Version
	}
	_ = s.Close()
	if err := writeDreamSeen(state, seen); err != nil {
		t.Fatal(err)
	}
	if fresh, owed, _, err := dreamDue(state, time.Hour); err != nil || fresh != 0 || !owed.none() {
		t.Errorf("labelled base still due: fresh %d owed %+v %v", fresh, owed, err)
	}
	if hint := dreamHint(); hint != "" {
		t.Errorf("hint after labelling: %q", hint)
	}
}
