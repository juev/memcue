package recall

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	"github.com/juev/memcue/internal/store"
)

// fakeEmbedder answers with one fixed vector, or with an error, which is all the pass needs to know about the
// endpoint.
type fakeEmbedder struct {
	tag string
	vec []float32
	err error
}

func (f fakeEmbedder) Tag() string { return f.tag }

func (f fakeEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	return [][]float32{f.vec}, nil
}

// unit returns a vector whose cosine with the query axis (1,0) is first. It is deliberately not of unit length:
// the store normalizes what it keeps.
func unit(first float64) []float32 {
	second := math.Sqrt(1 - first*first)
	return []float32{float32(first * 2), float32(second * 2)}
}

const modelTag = "http://localhost:11434/api/bge-m3"

// meaningBase fills a base with unrelated facts: the target one, and nine others that stand far from the query.
func meaningBase(t *testing.T) (*store.Store, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	target, err := s.Add("Порог min_coverage поднят до 0.25: шумовых фактов стало вдвое меньше при том же числе полезных.",
		[]string{"coverage"}, "memcue 2026-10-20")
	if err != nil {
		t.Fatal(err)
	}
	others := []string{
		"Публичный Caddy монтирует каталог configs/caddy и перечитывает конфиг только с полным путём.",
		"Gradle вне make требует JAVA_HOME, иначе сборка не находит нужный JDK.",
		"Issue #20 в hledger-lsp: Neovim format-on-type не двигает курсор после TextEdit.",
		"Эксперимент byte-vs-BPE на vast.ai: арендовать RTX 4090 и запускать обучение на сутки.",
		"Прокси mihomo на порту 1080 смешанный: HTTP и SOCKS на одном порту.",
		"Чекаут /opt/loffe не оставлять грязным после мержа — вернуть на ветку main.",
		"Крон microapp-nightly ходит каждые четыре часа и доставляет в свой топик.",
		"SSH-ключ hermes работает и на GitHub, и на Forgejo через порт 443.",
		"Генерация изображений идёт по подписке и складывает файлы в кеш.",
	}
	for i, content := range others {
		f, err := s.Add(content, nil, "test 2026-10-20")
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveVector(f.ID, modelTag, store.TextHash(content), unit(0.25+float64(i)*0.01)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveVector(target.ID, modelTag, store.TextHash(target.Content), unit(0.95)); err != nil {
		t.Fatal(err)
	}
	return s, target.ID
}

func TestVectorCandidatesFindTheParaphrase(t *testing.T) {
	s, target := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	hits, err := vectorCandidates(s, "почему стало меньше ерунды в подсказках", Default, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != target {
		t.Fatalf("hits = %v, want the one paraphrase", ids(hits))
	}
	if hits[0].Source != "vector" || hits[0].Meaning < 0.9 {
		t.Errorf("hit = %+v, want source=vector and a high similarity", hits[0])
	}
}

func TestVectorCandidatesSkipAnotherText(t *testing.T) {
	s, target := meaningBase(t)
	if _, err := s.Correct(target, "Совсем другой текст факта, исправленный позже.", nil); err != nil {
		t.Fatal(err)
	}
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	hits, err := vectorCandidates(s, "почему стало меньше ерунды в подсказках", Default, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == target {
			t.Fatalf("a vector of another text was compared: %v", ids(hits))
		}
	}
}

func TestVectorCandidatesIgnoreAnotherModel(t *testing.T) {
	s, _ := meaningBase(t)
	e := fakeEmbedder{tag: "https://routerai.ru/api/v1/qwen3-embedding-8b", vec: []float32{1, 0}}
	hits, err := vectorCandidates(s, "почему стало меньше ерунды в подсказках", Default, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v: vectors of another model must be ignored", ids(hits))
	}
}

func TestVectorCandidatesSkipSeenFacts(t *testing.T) {
	s, target := meaningBase(t)
	p := Default
	p.Seen = map[string]bool{target: true}
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	hits, err := vectorCandidates(s, "почему стало меньше ерунды в подсказках", p, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == target {
			t.Fatalf("a seen fact was returned: %v", ids(hits))
		}
	}
}

// A pool reads only the vectors of the most recent facts: the old target is out at pool 3 and back when the cap is off.
func TestVectorCandidatesRespectThePool(t *testing.T) {
	s, target := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	p := Default
	p.VectorPool = 3
	hits, err := vectorCandidates(s, "почему стало меньше ерунды в подсказках", p, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range hits {
		if h.ID == target {
			t.Fatalf("a fact outside the pool was scored: %v", ids(hits))
		}
	}
	p.VectorPool = 0 // no cap
	hits, err = vectorCandidates(s, "почему стало меньше ерунды в подсказках", p, e, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != target {
		t.Fatalf("hits = %v, want the old target when the pool is off", ids(hits))
	}
}

func TestVectorCandidatesFailOpen(t *testing.T) {
	s, _ := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, err: errors.New("endpoint is down")}
	if _, err := vectorCandidates(s, "любой вопрос", Default, e, 10); err == nil {
		t.Fatal("a dead endpoint must return an error for the caller to ignore")
	}
}

func TestRecallJudgeCanPromoteAVectorCandidate(t *testing.T) {
	s, target := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Порог": 0.95}}
	hits, err := RecallJudge(s, "почему шумно в ответах", "", rankedParams(), r, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || hits[0].ID != target || hits[0].Source != "vector" {
		t.Fatalf("hits = %v, want the vector fact first", ids(hits))
	}
	if hits[0].Meaning < 0.9 {
		t.Errorf("Meaning = %v, want the judge score", hits[0].Meaning)
	}
}

func TestRecallJudgeWithoutARankerStaysLexical(t *testing.T) {
	s, _ := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := RecallJudge(s, prompt, "", Default, nil, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plain) || (len(hits) > 0 && hits[0].ID != plain[0].ID) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
}

func TestRecallJudgeFallsBackWhenTheJudgeFails(t *testing.T) {
	s, _ := meaningBase(t)
	e := fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := RecallJudge(s, prompt, "", Default, &fakeRanker{min: 0.5, err: errors.New("down")}, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plain) || (len(hits) > 0 && hits[0].ID != plain[0].ID) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
}

func TestRecallJudgeFallsBackWhenEmbeddingsFail(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	e := fakeEmbedder{tag: modelTag, err: errors.New("down")}
	hits, err := RecallJudge(s, prompt, "", Default, &fakeRanker{min: 0.5, scores: map[string]float64{"Gradle": 0.9}}, e)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(plain) || (len(hits) > 0 && hits[0].ID != plain[0].ID) {
		t.Fatalf("hits = %v, lexical = %v", ids(hits), ids(plain))
	}
}

// BenchmarkVectorCandidates10k measures the vector pass of a prompt hook on a 10k-fact, 128-dimension base: the pool
// keeps the decode and the dot products bounded, while 0 scores every stored vector.
func BenchmarkVectorCandidates10k(b *testing.B) {
	const facts, dim, batch = 10000, 128, 500
	s, err := store.Open(filepath.Join(b.TempDir(), "facts.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	query := make([]float32, dim)
	query[0] = 1

	b.StopTimer()
	for start := 0; start < facts; start += batch {
		inputs := make([]store.FactInput, 0, batch)
		for i := start; i < start+batch && i < facts; i++ {
			inputs = append(inputs, store.FactInput{Content: fmt.Sprintf("Факт %d о порте %d демо-базы.", i, 5000+i),
				Keywords: []string{"порт"}, Origin: "bench 2026-10-20"})
		}
		added, err := s.AddBatch(inputs)
		if err != nil {
			b.Fatal(err)
		}
		for j, f := range added {
			vec := make([]float32, dim)
			vec[j%dim] = 1
			if err := s.SaveVector(f.ID, modelTag, store.TextHash(f.Content), vec); err != nil {
				b.Fatal(err)
			}
		}
	}
	e := fakeEmbedder{tag: modelTag, vec: query}
	prompt := "какой порт у демо-базы"

	for _, pool := range []int{2000, 0} {
		b.Run(fmt.Sprintf("pool=%d", pool), func(b *testing.B) {
			p := Default
			p.VectorPool = pool
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := vectorCandidates(s, prompt, p, e, 10); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

// Enabling the judge must not drop the host tie-break: two facts with the same score are ordered by place, and the
// displacement flag names the host fact left out behind another host's fact.
func TestRecallJudgeKeepsTheHostTieBreak(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const text = "Порог покрытия поднят до 0.25 для поиска фактов."
	other, err := s.AddWith(text, nil, "demo 2026-10-20", "", store.Meta{Scope: "host:other"})
	if err != nil {
		t.Fatal(err)
	}
	current, err := s.AddWith(text, nil, "demo 2026-10-21", "", store.Meta{Scope: "host:box"})
	if err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"Порог": 0.9}}
	p := rankedParams()
	p.MaxFacts = 1

	p.Host = "box"
	hits, err := RecallJudge(s, "порог покрытия поиск фактов", "", p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != current.ID {
		t.Fatalf("hits = %v, want the fact of this host %s", ids(hits), current.ID)
	}
	if hits[0].Displaced {
		t.Error("a served fact of this host is marked displaced")
	}

	p.Host = "other"
	hits, err = RecallJudge(s, "порог покрытия поиск фактов", "", p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != other.ID {
		t.Fatalf("hits = %v, want the fact of this host %s", ids(hits), other.ID)
	}
	if hits[0].Displaced {
		t.Error("a served fact of this host is marked displaced")
	}
}

// A foreign fact with a higher score wins on rank, not on place; when it takes the last slot while a fact of this
// host is left out, the log must say so.
func TestRecallJudgeMarksDisplacement(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "facts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	foreign, err := s.AddWith("Порог покрытия поднят до 0.95 для поиска фактов.", nil, "demo 2026-10-20", "",
		store.Meta{Scope: "host:other"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWith("Порог покрытия поднят до 0.25 для поиска фактов.", nil, "demo 2026-10-21", "",
		store.Meta{Scope: "host:box"}); err != nil {
		t.Fatal(err)
	}
	r := &fakeRanker{min: 0.5, scores: map[string]float64{"0.95": 0.95, "0.25": 0.8}}
	p := rankedParams()
	p.MaxFacts = 1
	p.Host = "box"
	hits, err := RecallJudge(s, "порог покрытия поиск фактов", "", p, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != foreign.ID {
		t.Fatalf("hits = %v, want the higher-scored foreign fact %s", ids(hits), foreign.ID)
	}
	if !hits[0].Displaced {
		t.Error("the omitted fact of this host did not mark the served foreign fact as displaced")
	}
}
