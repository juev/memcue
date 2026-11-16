# Answer quality on the compact search line, November 2026 (#756)

**On OpenCode 2.3.0 with DeepSeek Chat, the compact line `id@version scope YYYY-MM-DD content` gave the same answers
as the pretty-printed JSON on all nine tasks: 45 of 45 sessions per arm scored full marks with a blind judge, and
every correction, retirement and shelf search that a check could verify passed in both arms.** The tasks did not
separate the arms: this is a ceiling, not a proof that the two texts are equal on harder questions or weaker models.
The report answers the follow-up recorded on #756: the earlier measurement
([tool-rendering-756-2026-11.md](tool-rendering-756-2026-11.md)) only showed the text got shorter.

## Data

**Host.** OpenCode 2.3.0, `opencode run --standalone --auto --format json --model deepseek/deepseek-chat`. Each
session runs in its own home and on its own copy of the base, so a correction in one session never reaches the next.

**Model.** `deepseek/deepseek-chat` through an explicit provider entry in the session's config. A session counts only
when the session store of that run shows every answer came from this model; all 90 did.

**Arms.** `json`: the OpenCode plugin of `main` (`JSON.stringify(answer, null, 2)`). `compact`: the plugin of the
branch, with the day restored to the line (d5d81e0). Both arms use the same binary and base.

**Base.** Ten synthetic facts: seven for the repository of the session (`example.invalid/acme/orbit`), two for
another repository (`example.invalid/acme/comet`), one on the research shelf. Their `created` days range from
2025-03-04 to 2026-11-02; two facts about where CI runs disagree and differ only in their day.

**Tasks.** Nine, each with one prompt prefixed by "Check long-term memory with the memcue tools before you answer.
Answer in at most three sentences.":

| Task | What the model must do |
|---|---|
| read-port | Pick the staging port of this repository (6543), not the local one or the other repository's. |
| read-decision | Report a stored decision (no nightly images). |
| read-pitfall | Apply a stored pitfall (`-p 2`). |
| stale | Choose between two conflicting CI facts by their day. |
| correct | Correct the port fact to 6600 through `memcue_correct`, without a new fact. |
| retire | Retire one named fact and nothing else. |
| shelf | Find a finding that only a shelf search returns (the prompt asks for research findings). |
| widened | Say that the only Sentry fact belongs to another repository rather than claim it. |
| absent | Say that memory holds nothing about on-call, without inventing names. |

**Grading.** Five repetitions per task and arm, 90 sessions. Deterministic checks read what each session left in its
base (correct, retire) and its tool calls (shelf). A judge (`claude-opus-5-5`, one call per task, $2.61 in all) read
the seeded facts, the reference answer and the ten answers of that task in a shuffled order, without the arm, and
scored each 0, 1 or 2.

## Results

[quality-results.json](tool-rendering-756/quality-results.json), [quality.py](scripts/toolrendering756/quality.py):

| Task | json: judge | compact: judge | json: check | compact: check | json: median steps / input tokens | compact: median steps / input tokens |
|---|---:|---:|---:|---:|---:|---:|
| read-port | 10/10 | 10/10 | – | – | 2 / 15 249 | 2 / 15 135 |
| read-decision | 10/10 | 10/10 | – | – | 2 / 15 215 | 4 / 30 788 |
| read-pitfall | 10/10 | 10/10 | – | – | 2 / 15 162 | 2 / 15 215 |
| stale | 10/10 | 10/10 | – | – | 3 / 23 927 | 4 / 31 475 |
| correct | 10/10 | 10/10 | 5/5 | 5/5 | 3 / 23 214 | 3 / 22 991 |
| retire | 10/10 | 10/10 | 5/5 | 5/5 | 3 / 22 967 | 3 / 22 853 |
| shelf | 10/10 | 10/10 | 5/5 | 5/5 | 2 / 15 311 | 2 / 15 214 |
| widened | 10/10 | 10/10 | – | – | 4 / 31 094 | 2 / 15 255 |
| absent | 10/10 | 10/10 | – | – | 3 / 23 075 | 2 / 15 152 |

What the checks cover, task by task:

- **Mutating from what the model read.** Every `correct` and `retire` session in both arms called the tool with the
  id and the expected version it read (`@1` in the compact line, `"version": 1` in the JSON), left exactly the
  intended change in the base, and added no fact.
- **The day.** In `stale` every answer in both arms named the two days or the recency as the reason for GitHub
  Actions; the restored day is what the compact arm relied on.
- **The trailer.** In `widened` every compact answer said the Sentry fact belongs to another repository; the
  `widened` word and the binary's hint in the trailer carried that as the JSON field did. The `shelf` task did not
  test the shelf hint: its prompt asked for research findings, and every session in both arms named
  `shelf:research` in its first search, so no session read the hint before searching the shelf.

The input side of a session follows the number of model steps, since every step resends a prompt of about 7 500
tokens; the search result itself is a small share of it. In sessions of two steps, the compact arm's median is 81
tokens lower (15 161 against 15 242, 19 and 23 sessions). The step counts differ in both directions (more in compact
on read-decision and stale, more in json on widened and absent); in read-decision the extra compact steps were
checks of the empty working tree (`grep`, `ls`), with the same answer. Five repetitions do not tell such variation
from an effect of the text.

## A side finding: changes nobody asked for

In five of the ten `stale` sessions the model changed memory although the prompt only asked a question: it retired
the Jenkins fact (three json, one compact), corrected it to say it was superseded (one compact), or also bumped the
GitHub Actions fact (one json). It happened in both arms, so it is not caused by the rendering; it is a behaviour of
the instruction and the model, worth its own issue.

## What this does not measure

- Acting on the shelf hint of the trailer: the shelf task's prompt led every session to the shelf directly (see
  above); a prompt that does not mention research findings is needed for that.
- Harder discrimination. Every task was answered fully by both arms; questions where the facts are long, clipped
  (`…`) or many would be needed to see a difference.
- Other models and hosts. The free OpenCode models ran out of quota during the first attempt, the Z.ai GLM plan had
  expired and the OpenAI login answered 401 in an isolated home, so one paid model was measured. Pi, DSH and Hermes
  share the text (`plugins/transport/conformance/render-vectors.json`) but not the surrounding prompt; they were not
  run here.
- The Hermes-only parts (`(below the floor)`, its empty-answer wording) and the diagnostics in metadata.
- A first attempt that is not reported: without an explicit provider entry, an isolated OpenCode home answered every
  "deepseek" and "glm" session with its own free models. Those 162 sessions were discarded; the runner now records the
  serving model and counts a session only when it matches.

## Reproduction

```sh
python3 docs/research/scripts/toolrendering756/quality.py run --repo . --workdir /private/tmp/q756 \
    --models deepseek/deepseek-chat --reps 5 --jobs 6
python3 docs/research/scripts/toolrendering756/quality.py judge --workdir /private/tmp/q756
python3 docs/research/scripts/toolrendering756/quality.py report --workdir /private/tmp/q756 --out results.json
```

The runner builds the binary, seeds the synthetic base, copies both arms of `plugins/opencode` out of the checkout
(only `src` differs), and gives every session an isolated `HOME`/`XDG_*` with `MEMCUE_AUTO_INSTALL=0` and every
caller `MEMCUE_*` variable dropped. It needs the machine's OpenCode `auth.json` with a DeepSeek credential and a
logged-in `claude` for the judge. No live memory base is read or written.
