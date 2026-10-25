package extract

import (
	_ "embed"
	"strings"
)

// Action is one change a dream asks for: retire a fact, or correct it with new content.
type Action struct {
	Op       string   `json:"op"` // "retire" or "correct"
	ID       string   `json:"id"`
	Content  string   `json:"content,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Queries  []string `json:"queries,omitempty"` // correct: the search queries of the new text
	By       string   `json:"by,omitempty"`      // retire: the fact of the group that covers or outdates this one
	Kind     string   `json:"kind,omitempty"`    // retire: "duplicate" or "outdated"
	Reason   string   `json:"reason"`
}

//go:embed dream_packet.md
var packetPrompt string

//go:embed dream_prompt.md
var dreamPrompt string

// answerShape is the one answer a packet takes, with the packet id the model must echo.
const answerShape = "## Форма ответа\n\n" +
	"Ответ — один JSON-объект, без пояснений и без markdown. `packet` — ровно `%s`.\n\n" +
	"```json\n" +
	`{"packet": "%s",` + "\n" +
	` "splits": [{"id": "…", "parts": ["…", "…"], "reason": "…"}],` + "\n" +
	` "expansions": [{"id": "…", "queries": ["…", "…"]}],` + "\n" +
	` "actions": [{"op": "retire|correct", "id": "…", "content": "…", "keywords": ["…"], "queries": ["…"], "by": "…", "kind": "duplicate|outdated", "reason": "…"}],` + "\n" +
	` "translations": [{"id": "…", "translation": "…"}]}` + "\n" +
	"```\n\n" +
	"- `splits` — задача 1, только по фактам из «Данные: разделение».\n" +
	"- `expansions` — задача 2, только по фактам из «Данные: поисковые запросы»; разделённые факты сюда не включай.\n" +
	"- `actions` — задача 3, только по фактам из «Данные: группы». У `correct` дай и `queries` для нового текста по " +
	"правилам задачи 2: старые запросы снимаются вместе со старым текстом.\n" +
	"- `translations` — задача 4, только по фактам из «Данные: перевод».\n"

// DreamInstructions is the text a dream packet starts with: what to do, the four tasks with their rules, and the
// form of the answer to packet id.
func DreamInstructions(id string) string {
	var b strings.Builder
	b.WriteString(packetPrompt)
	b.WriteString("\n## Задача 1. Разделение составных фактов\n\n" + splitPrompt)
	b.WriteString("\n## Задача 2. Поисковые запросы\n\n" + expandPrompt)
	b.WriteString("\n## Задача 3. Группы похожих фактов\n\n" + dreamPrompt)
	b.WriteString("\n## Задача 4. Перевод\n\n" + translatePrompt)
	b.WriteString("\n" + strings.ReplaceAll(answerShape, "%s", id))
	return b.String()
}
