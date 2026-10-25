# The Hermes plugin on a live installation: adoption, the price of an answer, and the thin-answer hint, October 2026

Status: **measurement, 2026-10-26.** Same installation as [pull-hermes-014](pull-hermes-014-2026-10.md); 0.14.0 binary,
and the plugin at `main` — the version merged as #421. Everything below is the plugin's own surface: what the model gets,
what it costs, and what it does with the flags. The binary's search behaviour is priced in the earlier notes and is not
re-measured here.

Verdict in one line: **the plugin is cheap, live and well covered by its suite; the memory it delivers is only as good
as the base, and the model's own habits — `limit` 10 in half the calls, no control ever — cost more context than the
default assumes.**

## Setup

- Binary 0.14.0. Plugin `__init__.py` sha256 `cb0ce037be19…` is byte-identical to `origin/main` at `c4af1a9`; the
  gateway was restarted at 01:05 MSK on 2026-10-26, after the file was copied, and the hint was observed live in the
  session that opened after it: a query that returned one fact came back with
  `4 more facts hold the words but sit below the score floor`. The plugin's code does not need a file-level check after
  a restart — it needs one observation of the new answer.
- The base: **396 live facts by id** (`status='active'`, `review IN ('', 'provisional')`), `fact_index` 396 of 396,
  179 characters on average, one live fact past its expiry. Scopes: 267 `host:`, 58 `repo:`, 36 `service:`,
  27 `global`, 8 empty. Kinds: 173 `environment`, 132 `pitfall`, 23 `rule`, 23 `decision`, 15 `event`, 14 `repo`,
  7 `procedure`, 2 `other`, 7 empty.
- Two prompt arms, because they answer different questions:
  - **30 queries the model actually wrote**, taken from the live `search.log` and deduplicated. This is what the search
    gets in pull.
  - **20 user turns** from `state.db`: human sessions only (no cron, no subagent), longer than 45 characters,
    delegations, background-process notifications and gateway envelopes filtered out.
- Both arms read a **copy of the live base** with their own `MEMCUE_STATE_DIR`, through the installed provider itself
  (`MemcueMemoryProvider.handle_tool_call("memcue_search", …)`), so the flags, the `-in all` override, the scoping and
  the hint are the plugin's, not the binary's defaults. The wide arm is the same call with `min_score 0`.
- One rater, the agent that ran the calls, grading every delivered fact from its text alone: `U` answers the query,
  `R` is on the topic without answering, `N` is unrelated. Same definitions as the earlier notes.

## Tests

| test | result |
|---|---|
| plugin suite (`tests/test_provider.py`, 74 cases) | all pass, 5.3 s |
| call latency end to end, 90 calls (process start included) | p50 **9.8 ms**, p90 17.9, max 22.6 |
| — the same prompts straight to the binary | p50 8.8 ms, p90 10.7, max 12.7 |
| a thin answer, which pays a second search | **15.7–20.9 ms** against 8.3–10.3 for the same query wide |
| latency inside the binary, 30 logged live calls | p50 2.6 ms, p90 4.1 |
| facts per search, agent queries / user prompts | 3.57 / 4.00 (median 5 both) |
| characters per search | 780 ≈ **244 tokens** / 741 ≈ 232 |
| empty answers | 0 of 50 probes, 0 of 30 logged live calls |
| one-fact answers | 8 of 30 agent queries, 3 of 20 user prompts |
| hint fired | 8 of 30 and 3 of 20 |
| `limit` 5 against `limit` 10, same 30 queries | 3.61 facts / 780 chars against **5.13 / 1 086 ≈ 340 tokens** |
| card at session start | 15 rules, **2 880 characters ≈ 900 tokens** |

The plugin adds about a millisecond to a normal call: the version probe is cached per provider, the extra `-in all` is a
word. The second call is the only real addition, and it lands on the answers that were already thin.

## Adoption

- **Last 24 hours: 8 of 8 human sessions searched, 21 calls.** All eight are memory audits — the predecessors of this
  note. This number says nothing about ordinary work.
- **7 days: 26 of 56 human sessions (46 %).** Split by subject: the 29 sessions about memcue searched **18 (62 %)**;
  the other 27 searched **8 (30 %)**.
- **30 days: 26 of 108.** Excluding the memcue audits, **8 of 79 (10 %)** — and every one of those eight falls in the
  last week, so the plugin-era figure for ordinary work is the 30 % above and the 10 % is the pre-pull baseline.
- Cron and subagent sessions: 0. They get neither the tool nor the card by design and stay out of the denominator.
- Calls ever recorded: **57**. `limit` was passed in **29** of them (5 ten times, 10 thirteen times, 6 three times,
  8 once, 20 twice); `in`, `kind`, `since`, `min_score` in **none**.
- The sessions without a search include ordinary infrastructure questions the base can answer —
  "free space on the NAS and the size of the backups", "which model is the default in Hermes", "why did the server
  restart". The model answers those from the prompt and from shell commands.

## What an answer costs

The floor trims most answers below the limit, so the default is rarely the binding constraint: three quarters of the
probes returned five facts or fewer, and `limit 20` produced 13 at its widest. The price of a call is therefore normal:

- 244 tokens per search at the default, 340 at `limit 10`.
- The card is the larger constant: 900 tokens at every session start and after every compaction, 3.7 searches' worth.
- **4 of the 15 card entries repeat rules the memory tool block already injects** — the two "skills contain only
  procedures" entries come from one memory entry, and the anonymisation and research-in-the-repository rules are the
  memory tool's own wording. That is ~230 of the 900 tokens — a quarter of the card — spent on a second copy.
  The other 11 entries are the base's own (`source=user`, `agent`, empty), not the mirror.

## Quality

| | agent queries (30) | user prompts (20) |
|---|---|---|
| facts delivered | 107 | 80 |
| useful (U) | **37 (35 %)** | **17 (21 %)** |
| on topic (R) | 52 (49 %) | 30 (38 %) |
| unrelated (N) | **18 (17 %)** | **33 (41 %)** |
| prompts with a useful fact | **26 of 30 (87 %)** | **13 of 20 (65 %)** |

The shares are this note's own baseline, not a regression against [pull-hermes-014](pull-hermes-014-2026-10.md): that
note graded 8 hand-written queries and 20 user prompts on a base five facts smaller, and its 4 % unrelated share came
from queries written to be answerable. Here the query set is whatever the model asked over one day, and a third of it is
about memcue itself, where the base is dense with near-neighbours.

The four agent queries without a useful fact are all the same shape: the base has the topic and not the question.

- "the measurements of the plugin test" — measurements are deliberately not stored: by the user's own rule they live
  in `docs/research`, so the five delivered facts describe the harness and none reports a number.
- "memory limit, batch, empty patch, content required" — the base answers about `state.db` columns and never about the
  memory tool's limits.
- "memcue default number of facts" — the wording of the fact is "the number is set by `search_facts`", which does not
  say what the default is.
- "plugin scope" — every fact about the scope was delivered; none of them states the default the model asked about.

### Correction: the useful bucket is half installation state

Re-read against the rule that the installation's version, paths and defaults are **read from the machine and never from
memory** — a version fact rots with the next release, and the command that answers it is one call — the grades above
overstate the base's usefulness:

| among the 54 useful deliveries | deliveries | distinct facts |
|---|---|---|
| installation state (version, wrapper and its variables, `search_facts`, this release's flags and expiry, suite path) | **24** | 13 |
| repository documentation (release workflow, the dsh plugin, the search stand) | 5 | 3 |
| belongs in memory: rules, decisions, pitfalls, procedures | **25** | 17 |
| all useful deliveries | 54 | 33 |

So the base delivered **25 useful facts out of 187 (13 %) that belong in memory**, and 1.6 copies of each distinct fact.
The re-grade also reverses the conclusion drawn from it: the defect is not that stale state ranks first, it is that
installation state is stored at all — a fresh copy of "the version is 0.14.0" is the wrong channel just as much as an
old one. The measurement itself stands (the search returns what the words match); the judgement of what it returned
does not.

## The thin-answer hint (#421)

- It fires on **8 of 30** agent queries and **3 of 20** user prompts, and it fires exactly where the floor is the
  visible cause: those answers held one fact and the wide search held more.
- It never fires when nothing is held (an unrelated query returns nothing and no hint) and never when the model already
  asked wide (`min_score` 0).
- **The count saturates at the limit.** The wide arm is capped by `-n` too, so the hint reports
  `limit − served` at most: `limit 5` → "4 more" on every call, `limit 10` → "9 more", `limit 20` → "19 more", for a
  query whose tail is 30 facts long. With the model's habit of asking for 10, the message says 9 — again an artefact.
  `(limit still caps the answer)` is in the text, but a model reading "9 more" plans a shallower retry than it needs.
- **What the hint points at is mostly tangential.** Across the eight thin answers, 32 facts sat below the floor; the
  same rater graded **2 of 32 (6 %)** as answering the query — the `skill_manage` refusal to create over an existing
  directory, and the Open VSX publication delay. The rest are facts on the words: "the plugin from the tag", "the state
  directory", "`MEMCUE_FACTS`", one different repository. Reading them costs a turn and a second answer.
- Its case is therefore narrow and real: when the floor hides the *answering* fact, the hint is what tells the model a
  fact exists. In this sample that case is 2 in 8.

## Where the search looks

The plugin still appends `-in all` when the model names no place, and on this base it is the difference between an
answer and nothing: the same host question from a repository workspace returned **3 facts** with the plugin's default,
**0** with `in=project` and **3** with `in=all` — 267 of 396 live facts carry the host scope. Inside the memcue
repository itself, `in=project` returned 1 of 3.

## Writing: extraction and vet

- **73 threshold runs since 19 October wrote 177 facts**, with **131 candidate facts rejected by the policy** — 42 % of
  what the extractor proposed, mostly for evidence that was not in a `RESULT` block or for a source that was neither
  the user nor a tool. 3.6 M characters went through, at 1.34 M input and 0.59 M output tokens.
- `vet` decided 213 times: 131 approved, 69 retired, 13 split (one split failed); the queue held **5 candidates** at
  measurement, so the pipeline drains as fast as it fills. The candidate count by a hand-written SQL over
  `max(version)` was 128: the CLI's 5 is the number to quote — most of the 128 are mirror rows of the memory tool that
  the vet resolves, not a queue.

## What is good

1. **It is cheap and it is live.** 9.8 ms p50 per call end to end, 2.6 ms inside the binary, 0 empty answers in 50
   probes and in 30 logged live calls.
2. **The tool surface behaves.** `limit` must be a positive integer (0, -3 and "x" are refused), `in`/`kind`/`words`
   come back as errors that list the valid values, `limit` above 50 clamps to 50, and a missing binary or an old one
   degrades to "search still works, card and extraction off" instead of failing the turn.
3. **The scope override earns its keep** (#419): without `-in all` the host is invisible from a repository.
4. **The suite covers the contract**, not the implementation: 74 cases, 5 s, including the scope override, the hint,
   the version gates and the usage log being off by default.
5. **The write path works**: 177 facts a week, 5 candidates pending, no queue growth.

## What is not

1. **The hint's number is an artefact of the limit** — "4 more" on every default call, whatever the tail.
2. **The tail it advertises is 6 % useful**, so the hint buys a second reading mostly of neighbours.
3. **Adoption outside audits is 30 % in a week, 10 % over a month.** Ordinary questions that the base answers run
   without a search.
4. **The model pays for its own habit**: `limit` 10 in 29 of 57 calls, +40 % tokens per call, and above the floor the
   extra slots stay empty — the widest answer seen was 13 of 20.
5. **No call ever used a control** (`in`, `kind`, `since`, `min_score`): the schema carries them, the habit does not.
6. **The card repeats the memory tool**: 4 of 15 rules, one rule twice, ~230 of 900 tokens.
7. **A bad `min_score` leaks the binary's usage text** as the tool error; the other arguments are validated.
8. **The base stores what a command should answer.** 13 distinct facts of the useful bucket describe the installation
   — the version, the wrapper and its variables, `search_facts`, this release's flags and expiry — and 49 live facts
   name a version number. Freshness is a symptom, not the defect: even a correct copy is the wrong channel, and these
   are the facts that take the top slots on the most frequent question of the week.

## Recommendations, by payoff

1. **Count the tail honestly, or not at all.** Run the wide arm with `-n min(4 × limit, 20)` and phrase the hint
   "at least N more hold the words", or drop the number: today every default call says exactly four, which is the
   limit talking, not the base.
2. **Follow the model's own choice of `limit`.** It asks for 10 in half the calls; at 10 the answer is 340 tokens and
   5.1 facts against 244 and 3.6 at 5. Either make 10 the default and measure again, or put "raise the limit only when
   the answer is empty" in the schema — the current split pays the 10-fact price for the 5-fact answer.
3. **Stop the card repeating the memory tool.** Skip `source=memory` facts in `system_prompt_block()` and merge a rule
   that two entries carry: ~230 of the 900 characters are a second copy of what the prompt already has.
4. **Put the trigger where the model reads it.** The instruction says to search "before answering a question that may
   depend on earlier sessions"; it does not name the classes that actually recur — host paths, installed versions,
   service configuration, past decisions — and adoption outside audits is 30 %. One sentence with those four classes
   is cheaper than any mechanism.
5. **Base hygiene, not code — and it is a rule, not a clean-up.** Retire the installation-state facts (version, sha,
   wrapper variables, config defaults, release behaviour) and keep the extractor from storing them: 13 distinct facts
   in the useful bucket, 49 live facts naming a version. What stays is what a command cannot answer — rules, decisions,
   pitfalls, procedures — and the search already has the lever (`-since`, `-kind`) that nobody uses.

## Filed from this note

- **#423** — the hint reports `limit − served`, not the tail: 4, 9 and 19 for the same one-fact answer at limits 5, 10
  and 20.
- **#424** — the card repeats 4 of the 15 rules of the memory tool block, one rule twice, ~230 of 900 tokens.
- **#425** — the default `limit`: 29 of 57 calls pass 10, +40 % tokens per call, no control ever used. The issue asks
  for a decision, because the measurement alone does not say which default is right.
- **#426** — installation state is stored as facts (13 distinct in the useful bucket, 49 live facts naming a version);
  the fix belongs in the extraction prompts and the vet policy, not in the search.

## Deliberately not filed

- The adoption trigger: "search before answering, and name the classes that recur" is a wording experiment, not a
  defect; it goes to a PR with a before/after count if the decision is taken.
- #417 (the floor on long queries) and #418 (a wrong `-in repo:KEY`) stay open and are untouched here.

## Reproduction

```sh
# the plugin's answer, not the binary's: same call the model makes, on a copy of the base
cp facts.db /tmp/facts.db
python3 - <<'EOF'
import importlib.util, json
spec = importlib.util.spec_from_file_location("p", "$HERMES_HOME/plugins/memcue/__init__.py")
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
p = m.MemcueMemoryProvider({"binary": "$HERMES_HOME/bin/memcue", "facts": "/tmp/facts.db",
                            "state_dir": "/tmp/state", "host": "<host>"})
p.initialize("probe", hermes_home="/tmp/home", agent_context="primary"); p._workspace = "/tmp/work"
for q in ["<query the model wrote>"]:
    print(json.loads(p.handle_tool_call("memcue_search", {"query": q})))          # default arm
    print(json.loads(p.handle_tool_call("memcue_search", {"query": q, "min_score": "0"})))  # the tail
EOF
```

Not measured: a second labeller, another installation or base, the Claude Code and dsh plugins (they share the binary,
not the provider), and the cost of the extraction calls in money — the tokens are in `extract.log`, the price depends
on the provider the installation points at.
