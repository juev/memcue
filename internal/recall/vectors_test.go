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

func TestMeaningPassAddsWhatTheWordsMiss(t *testing.T) {
	s, target := meaningBase(t)
	vp := VectorParams{Embedder: fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}, Margin: DefaultVectorMargin}
	// The words of this prompt share nothing with any fact: the lexical pass would find no candidate at all.
	hits, err := byMeaning(s, "почему стало меньше ерунды в подсказках", Default, vp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != target {
		t.Fatalf("hits = %v, want the one paraphrase", ids(hits))
	}
	if hits[0].Meaning < 0.9 {
		t.Errorf("Meaning = %.3f, want the similarity", hits[0].Meaning)
	}
}

func TestMeaningPassYieldsToTheWords(t *testing.T) {
	s, _ := meaningBase(t)
	vp := VectorParams{Embedder: fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}, Margin: DefaultVectorMargin}
	existing := []Hit{
		{Fact: store.Fact{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAV"}, Coverage: 0.5, Matched: 3},
		{Fact: store.Fact{ID: "01ARZ3NDEKTSV4RRFFQ69G5FAW"}, Coverage: 0.4, Matched: 3},
	}
	hits, err := byMeaning(s, "любой вопрос", Default, vp, existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(existing) {
		t.Fatalf("hits = %v: the vector pass must not take a slot the words filled", ids(hits))
	}
}

func TestMeaningPassNeedsAComparableVector(t *testing.T) {
	s, target := meaningBase(t)
	// The fact was corrected after its vector was made: the text hash no longer matches, so the vector is foreign.
	if _, err := s.Correct(target, "Совсем другой текст факта, исправленный позже.", nil); err != nil {
		t.Fatal(err)
	}
	vp := VectorParams{Embedder: fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}, Margin: DefaultVectorMargin}
	hits, err := byMeaning(s, "почему стало меньше ерунды в подсказках", Default, vp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v: a vector of another text must never be compared", ids(hits))
	}
}

func TestMeaningPassIgnoresAnotherModel(t *testing.T) {
	s, _ := meaningBase(t)
	vp := VectorParams{Embedder: fakeEmbedder{tag: "https://routerai.ru/api/v1/qwen3-embedding-8b"}, Margin: DefaultVectorMargin}
	hits, err := byMeaning(s, "почему стало меньше ерунды в подсказках", Default, vp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v: vectors of another model must be ignored", ids(hits))
	}
}

func TestMeaningPassSkipsWhatTheSessionSaw(t *testing.T) {
	s, target := meaningBase(t)
	p := Default
	p.Seen = map[string]bool{target: true}
	vp := VectorParams{Embedder: fakeEmbedder{tag: modelTag, vec: []float32{1, 0}}, Margin: DefaultVectorMargin}
	hits, err := byMeaning(s, "почему стало меньше ерунды в подсказках", p, vp, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v: a fact the session already got must not come again", ids(hits))
	}
}

func TestFailedEndpointLeavesRecallAsItWas(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Настройки approvals.deny блокируют пуш?"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	vp := VectorParams{Embedder: fakeEmbedder{tag: modelTag, err: errors.New("endpoint is down")}}
	hybrid, err := RecallHybrid(s, prompt, "", Default, vp)
	if err != nil {
		t.Fatalf("a dead endpoint must not fail recall: %v", err)
	}
	if len(hybrid) != len(plain) {
		t.Fatalf("hybrid = %v, lexical = %v", ids(hybrid), ids(plain))
	}
	for i := range plain {
		if hybrid[i].ID != plain[i].ID {
			t.Fatalf("hybrid = %v, lexical = %v", ids(hybrid), ids(plain))
		}
	}
}

func TestNoEmbedderIsTheOldBehaviour(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "почему стало меньше ерунды в подсказках"
	plain, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	hybrid, err := RecallHybrid(s, prompt, "", Default, VectorParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hybrid) != len(plain) {
		t.Fatalf("hybrid = %v, lexical = %v", ids(hybrid), ids(plain))
	}
}

func ids(hits []Hit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ID
	}
	return out
}

func TestOnlyModeIgnoresTheWords(t *testing.T) {
	s, _ := meaningBase(t)
	// The words alone would find this fact; the vector gate finds nothing for the prompt, and in only mode that
	// nothing is the answer.
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	lexical, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	if len(lexical) == 0 {
		t.Fatal("the test needs a prompt the words do find")
	}
	vp := VectorParams{Only: true, Embedder: fakeEmbedder{tag: modelTag, vec: []float32{0, 1}}}
	hits, err := RecallHybrid(s, prompt, "", Default, vp)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("hits = %v: only mode must not consult the words", ids(hits))
	}
}

func TestOnlyModeFallsBackWhenTheEndpointFails(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	lexical, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	vp := VectorParams{Only: true, Embedder: fakeEmbedder{tag: modelTag, err: errors.New("down")}}
	hits, err := RecallHybrid(s, prompt, "", Default, vp)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(lexical) || (len(hits) > 0 && hits[0].ID != lexical[0].ID) {
		t.Fatalf("a dead endpoint must leave the words: %v vs %v", ids(hits), ids(lexical))
	}
}

func TestOnlyModeWithoutVectorsFallsBack(t *testing.T) {
	s, _ := meaningBase(t)
	prompt := "Gradle требует JAVA_HOME, иначе сборка не находит JDK"
	lexical, err := Recall(s, prompt, "", Default)
	if err != nil {
		t.Fatal(err)
	}
	vp := VectorParams{Only: true, Embedder: fakeEmbedder{tag: "https://elsewhere/v1/other-model", vec: []float32{1, 0}}}
	hits, err := RecallHybrid(s, prompt, "", Default, vp)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(lexical) {
		t.Fatalf("hits = %v, want the words %v", ids(hits), ids(lexical))
	}
}
