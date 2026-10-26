# Pull at 0.16.0 on a live installation: the check repeated, October 2026

Status: **interim measurement, 2026-10-26.** The same installation, the same twenty user prompts and ten
agent-style queries as [pull-hermes-014](pull-hermes-014-2026-10.md), two releases later. 0.16.0 is installed
(binary and plugin) and every call below goes through the installed plugin, not the binary, on a copy of the base.

Verdict in one line: **the search side is healthy and cheap — 413 of 413 facts indexed, 2.4 ms inside the binary,
12.9 ms per call end to end, 48 % of the facts an agent query receives answer it — and the two costs that remain are
at the edges: the card pays ~3 000 characters a turn for 18 rules, of which 6 restate the memory block, and on a
whole user turn the answer is noise (18 % useful, 10 of 20 prompts covered).**

## Setup

- memcue **0.16.0**, binary and plugin (`plugins/hermes/memcue`, plugin suite 81 tests).
- The base, by id (`status='active'`, `review IN ('', 'provisional')`): **413 live facts**, 183 characters on
  average, schema 5. Index **413 of 413 ids**, no row without a fact and no fact without a row. 8 candidates live
  (all `source=memory`, untyped and unscoped), 155 candidates retired. One live fact is past its expiry
  (`expires 2026-10-21`).
- Scope split: `host:loffe` 267, `repo:…/memcue` 37, `global` 30, **no scope 14**, `repo:…/research` 11,
  `repo:…/server-config` 10.
- One copy of the base at `/opt/data/cache/scratch/mc16/facts.db`, `MEMCUE_STATE_DIR` and `XDG_STATE_HOME` on
  scratch, the provider loaded from `/opt/data/plugins/memcue/__init__.py`, so every number is the plugin's
  behaviour and the live logs stay clean.
- Two query sets: **10 queries written the way the agent writes them** (phrasing taken from the live `search.log`,
  not from the audit sessions only), and **the 20 user prompts** of the 0.14 note, unchanged. One rater, one pass,
  `U` answers the query, `R` is the same subject, `N` is unrelated.

## Tests

| test | result |
|---|---|
| plugin suite (`tests/test_provider.py`) | 81 tests, all pass, 5.5 s |
| `go test ./internal/bench/` against the installed binary | PASS, p95 of 20 searches over 1 000 facts **12.8 ms** (median 8.1 ms) |
| latency end to end, 30 searches (process start included) | p50 **12.9 ms**, p90 15.2 ms, p95 16.2 ms |
| latency inside the binary, 74 logged calls of the live gateway | p50 **2.38 ms**, p90 4.07 ms, p95 4.96 ms |
| empty answers, live calls | 3 of 74 |
| facts per call, live | 5.58 average |

## Agent queries (10): the answer is small and mostly on point

| | 0.16.0 | 0.14.0 (other pass) |
|---|---|---|
| facts delivered | 29 | 26 |
| useful (U) | **14 (48 %)** | 11 (42 %) |
| same subject (R) | 10 | 14 |
| unrelated (N) | **5 (17 %)** | 1 (4 %) |
| queries with a useful fact | **8 of 10** | 8 of 8 |
| characters per call | **1 241** ≈ 450 tokens | 606 |

Two of the ten got nothing useful, and neither is a ranking error:

- "как обновить headscale" returned five facts, none about headscale — **the base holds no fact with the word**.
  An OR-term BM25 with no coverage gate answers a question the base cannot answer with whatever shares a stroke
  (#436 is the same observation from the other side: the base, not the floor, is the ceiling).
- "free space on the NAS, what weighs most" returned the two facts that name the restic repository and its path,
  which is where this answer starts, and no fact holds a size.

Where the answer is right it is very right: "stale_write_blocked" returns one fact, the one that names the tool and
the error; "hermes cron jobs.json" returns three facts that each answer a part of it.

## User prompts (20): on a whole turn the answer is mostly the same subject

| | 0.16.0 | 0.14.0 (other pass) |
|---|---|---|
| facts delivered | 77 | 65 |
| useful (U) | **14 (18 %)** | 22 (34 %) |
| same subject (R) | 34 | 23 |
| unrelated (N) | **29 (38 %)** | 20 (31 %) |
| prompts with a useful fact | **10 of 20** | 13 of 20 |
| characters per call | **1 614** ≈ 585 tokens | 550 |

The criterion is not the 0.14 pass's — another rater, another base (413 against 391 facts) — so the drop is a
direction, not a delta. The direction agrees with the note of #436: a user turn is long, every term it holds
inflates some fact, and the answer fills with facts that share the subject and answer nothing. Examples:

- "почему выбрал версию 0.10, дрим" → a rule about how the release number is chosen (R) and two facts about number
  terms of other releases (N).
- "что за сообщение про ошибку изменения файла" → the `write_file` fact (U) between two facts about the state
  database and one about deletion inside the venv (N).
- "как обычно работают с Hermes, только мессенджеры" → `/handoff` (R) and four facts about releases and the shelf.

The three prompts that are answered well are the ones that name a decision ("пушим в main, секреты не фильтруем"),
a rule ("почему в навыке текущие данные") or a task list; the vague ones ("провели обновление, как теперь
работает?") receive rules about the order of work, which is the base's most frequent subject.

## What the answer tells the model

- The floor hint (thin answer) fired on **4 of 10** agent queries and **4 of 20** user prompts: "19 more facts hold
  the words but sit below the score floor". Its arithmetic checks out: with a default limit of 5 the wide pass
  returns exactly 20 facts, and 19 is the tail of an answer of one.
- One prompt got the second form — "20 facts hold the words … the first 2 come with `below_floor: true`" — so the
  two factual guesses of #439 both appear on the live base, and neither is spurious.

## Adoption

- Seven days: **30 of 58** sessions with a human prompt called `memcue_search` at least once (52 %), 68 calls in
  33 sessions. Split by subject, the picture changes: **4 of 15** sessions on any other subject (27 %) called it,
  26 of 43 sessions whose subject is memcue itself. The memory is mostly used to talk about itself.
- Arguments, from `tool_calls` of the assistant messages: **34 of 68 calls named no control at all**; `limit` was
  named 32 times (5 ×10, 10 ×14, 6 ×4, 20 ×2, 8 ×2), `in` twice, `min_score` once.
- `usage.log`: 286 facts delivered over 81 records, **90 of them (31 %)** occur in the answer text — the plugin's
  own definition of "used" (an identifier from the answer in the reply).

## The card, and the base

- The card is **3 079 characters, 18 facts** at session start (`card.log`, `excluded 0`), and it is rebuilt after
  every compaction, so it is the constant price of the session next to the per-call price above.
- **6 of those 18 lines restate what `MEMORY.md`/`USER.md` already carry**, measured against the two files, and
  two of them (`01M5QHD2`, `01M5QM0K`) are one and the same rule about skills. The filter of #424 is in place —
  `excluded 0` means the binary found nothing to drop — and it does not catch a paraphrase: the same rule written
  with other words passes.
- Installation state survives in the base: **14 live facts name a version of memcue or Hermes**, the newest named
  is 0.14.0, and none names the installed 0.16.0. The vet rule of #426 stops new ones at the write; it does not
  retire the old ones.
- 8 live candidates are all `source=memory`, `kind=''`, `scope=''`: mirrors of the memory notes written before #440
  was fixed. They wait forever, vet never sees them, and the search never returns them.

## Errors, reproduced on the installed 0.16.0

| # | the defect | reproduction | result |
|---|---|---|---|
| [#442](https://github.com/juev/memcue/issues/442) | a fact with no scope belongs to no place | `search -in project "…"` against `search -in all "…"` | `[]` against two facts; 14 live facts are in this state |
| [#453](https://github.com/juev/memcue/issues/453) | the shelf hint is lost with `-json` | a shelf fact, `search "zqxshelf"` against `search -json "zqxshelf"` | plain: `1 fact on the shelf matches: -in shelf:research`; `-json`: `[]`; the plugin returns `{"facts": []}`, so the model never learns the shelf holds it |
| [#454](https://github.com/juev/memcue/issues/454) | a limit of 0 or less says the base holds nothing | `search -n 0 -json "…"`, `search -n -1 -json "…"` | `[]`, with no error and no hint |
| [#436](https://github.com/juev/memcue/issues/436) | a user message needs a ranker, not a floor | the 20 prompts above | 18 % useful, 38 % unrelated, coverage 10 of 20 |

What is not a defect, measured rather than read from code:

- **Expiry is filtered in `search`.** The live fact past its expiry does not come back, and `-expired` is the only
  way to see it. An earlier version of the skill said the opposite; corrected there.
- **The floor's arithmetic is right** (19 more when one of twenty is served; 3 more when one of four).
- **The shelf is empty** — 0 facts — so #453 today costs nothing in practice and will cost once research facts
  start landing on it. The config key `memory.memcue.shelf` is `research`.

## What is worse than at 0.14.0

1. **The card grew** — 3 079 characters and 18 rules, 6 of them already in the memory block; every new global rule
   adds 150–250 characters to every turn.
2. **The base lags the release** — the newest version it names is 0.14.0, and the facts that described earlier
   installations are still live.
3. **The memory is used for meta-work** — 26 of 30 sessions that call it are about memcue itself; on other
   subjects adoption is 27 %, so the quality numbers above are measured on queries nobody but an auditor writes.

## Reproduction

```sh
# the plugin's own behaviour on a copy of the base
cp /opt/data/memcue/facts.db $SCRATCH/facts.db
MEMCUE_FACTS=$SCRATCH/facts.db MEMCUE_STATE_DIR=$SCRATCH/state XDG_STATE_HOME=$SCRATCH/state \
  python3 probe.py prompts.json out.jsonl         # imports the provider from /opt/data/plugins/memcue
# the defects
MEMCUE_FACTS=$SCRATCH/facts.db memcue search -n 0 -json "…"
MEMCUE_FACTS=$SCRATCH/facts.db memcue search -json "zqxshelf"      # [] where the plain call hints the shelf
MEMCUE_FACTS=$SCRATCH/facts.db memcue search -in project "…"       # [] where -in all finds the fact
# the tests
env -u MEMCUE_FACTS -u MEMCUE_USAGE_LOG MEMCUE_BIN=/opt/data/bin/memcue-real \
  /opt/hermes/.venv/bin/python plugins/hermes/memcue/tests/test_provider.py
MEMCUE_BIN=/opt/data/bin/memcue-real go test ./internal/bench/ -count=1
```

Not measured: a second labeller, a base other than this one, and the queries of another harness. The twenty user
prompts are the same set as the 0.10 and 0.14 notes, so those notes compare in set but not in rater.
