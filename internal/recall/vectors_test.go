package recall

import (
	"context"
	"errors"
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

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}
