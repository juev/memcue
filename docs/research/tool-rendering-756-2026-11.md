# The text one search costs the model, November 2026 (#756)

**On OpenCode 2.3.0 the compact rendering of one five-fact answer cut the prompt of the step that follows the tool
result from a median of 6 912 to 6 576 tokens (336 tokens, 4.9% of that request), because the tool result itself fell
from 1 903 characters in 44 lines to 1 006 characters in 6 lines.** Both arms ran the same prompt, model, facts base
and binary; only the plugin's rendering differed. This is the share one search pays on every search; the
whole-plugin overhead measured in #752 (16 866 input units without the plugin against 18 345 with it) is a different
number and does not change here.

## Data

**Host.** OpenCode 2.3.0 (`brew opencode-v2`), run as `opencode run --standalone --auto --format json`, one private
server per run. The issue was measured on 2.2.7; this report is the installed version, and the tool-result contract it
relies on (`content` is model-facing, `metadata` is UI-only) is the same in both.

**Model.** `opencode/iceman-free`, the provider OpenCode offers without a credential. It answered with one
`memcue_search` call in every counted run; a run that deviated from one call or ended in a provider error was not
counted (one run hit a provider timeout after 180 s).

**Prompt.** One fixed message: *Make exactly one memcue_search call: query "quarry pipeline checks", limit 5. Do not
search again. Then reply with the id and version of every fact you received, one line each, and stop.*

**Facts.** Five synthetic facts in a throwaway text base, scope `global`, all five returned by the search. No private
fact or query text is in this repository; the measurement script seeds them.

**Binary.** `bin/memcue` built from the branch. The CLI, its argv and its `search -json` answer are untouched (#756
changes no CLI flag); both arms call the same binary.

**Metric.** `tokens.input + tokens.cache.read` of the step that follows the tool result, from the `step_finish` events
of `--format json`. The step before it (the search call) is identical in both arms, so the difference is the tool
result.

## Arms

1. **Before** — the plugin of `main`: `JSON.stringify(answer, null, 2)` in the tool content and a second text part
   for a native diagnostic.
2. **After** — the plugin of the branch: `renderSearch` from `memcue-transport`, one line per fact with
   `id@version`, the scope and the content, then one trailer line with the count and the hints.

## Results

[results.json](tool-rendering-756/results.json), [measure.py](scripts/toolrendering756/measure.py):

| Arm | tool result | step that reads it, tokens | runs |
|---|---:|---:|---|
| Before (JSON) | 1 903 characters, 44 lines | 6 897 / 6 928 / 6 913 / 6 910 | 4 |
| After (compact) | 1 006 characters, 6 lines | 6 571 / 6 587 / 6 576 | 3 |
| **Median** | **−47% characters, 44 → 6 lines** | **6 912 → 6 576, −336 (−4.9%)** | |

The rendering removed 897 characters and 336 tokens: the JSON paid for braces, repeated field names and a newline per
field, which tokenize worse than the compact lines. The four before-runs and the three after-runs are separate
sessions with separate standalone servers; the spread inside an arm is the provider's own accounting, and the two arms
do not overlap.

## What this does not measure

- The whole plugin overhead of #752: the instruction, the tool schemas and the tool result together. This report
  isolates the tool result, the part that repeats on every search.
- Any other model, host or fact set. A host that keeps its own rendering (Pi, DSH) gets the same form from the same
  vectors, but was not run against a provider here.
- Answer quality. The arms ask the model to repeat the ids and versions; whether a compact answer is read as well as a
  JSON one is not tested by this design.
- The old `memcue: no matching facts` wording of DSH and the empty-answer path: covered by the conformance vectors, not
  by a provider run.

## Reproduction

```sh
python3 docs/research/scripts/toolrendering756/measure.py \
    --repo . --before-ref main --after-ref HEAD --runs 3 \
    --opencode ~/bin/opencode --workdir /private/tmp/m756-measure
```

The script builds the binary, seeds the five facts in a throwaway base, copies both arms of `plugins/opencode` out of
the checkout (only `src` differs), writes an isolated `HOME`/`XDG_*` per run with `MEMCUE_AUTO_INSTALL=0`, and counts
the `step_finish` tokens. No live memory base is read or written.
