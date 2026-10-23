# Recall without a remote judge: local options, October 2026

Status: **literature and source review of 2026-10-23; its measurements are done, see [Outcome](#outcome).** The owner asked how memcue could stop calling a
remote model on the read path and decide locally, with the working model where that is possible, which facts a prompt
gets: global rules and what the user asked to remember always, within a budget; the repository's facts when they are
needed. This note collects what memcue's own measurements say the remote call buys, what other systems and the
literature offer in its place, and lays out an architecture with the order in which to measure it. Nothing here changes
a default; every "do" below is a hypothesis for an issue of its own.

## Method

Five parallel source surveys, one per sub-question: local rerankers and how to run them from a Go binary; distilling a
judge into a small model and learning to rank on a few thousand labelled pairs; what the Claude Code hooks and memory
features can and cannot do; how other coding agents route stored knowledge without a model call per prompt; and the
academic work on selective memory injection. Then seven of the load-bearing numbers were re-read in their primary
source by a second reader. About 90 pages were opened; a claim that rests on a search snippet or a secondary source is
marked *(unverified)*. memcue's own numbers come from the notes in this directory and [`docs/evaluation.md`](../evaluation.md);
none was re-measured.

## 1. What the remote call buys today

Three kinds of model call exist, and only one of them is paid for by the user waiting:

| Call | Model | When | Cost |
|---|---|---|---|
| recall rerank | Jev on RouterAI | every prompt with two or more lexical candidates: 23 % of 634 live prompts on the Mac ([storage](storage-2026-10.md)) | 0.37–0.53 s isolated, 1.1 s median live on the Mac, 0.9 s in the Hermes gateway, which pays a TLS handshake per process; 0.016 ₽ a request ([luna](luna-decisions-2026-10.md)) |
| dream triage | Jev | before a dream; opt-in, off by default | saves $0.1–0.2 per daily dream ([dream-triage](dream-triage-2026-10.md)) |
| extraction, vet, dream, split, query expansion | main model: `claude -p` or `MEMCUE_API_URL` | in the background, after a session | $0.18–1.33 a session on Opus, about $5 per million characters; DeepSeek about thirty times less |

Money is not the rerank's problem: 180 prompts cost $0.017. Its problems are the second provider and key, the second of
latency on the hot path, and the failure shape that retired holographic before memcue — a remote call on every turn that
times out and leaves the memory silent for a minute ([jev-judge](jev-judge-2026-10.md)). The background calls are where
the money goes, and that is a separate lever (the extraction model); this note is about the read path.

What the judge does, on the gold set of 180 prompts over the author's base ([one-model](one-model-2026-10.md)):

| Reader | Useful prompts (top-1 / any) | USEFUL | RELATED | NOISE |
|---|---|---|---|---|
| words, current gate | 43 / 52 | 72 | 31 | 68 |
| words, no gate, BM25 order | 71 / 90 | 122 | 91 | 128 |
| Jev over 10 gate-ordered candidates | 93 / 99 | 152 | 54 | 21 |
| Jev over 20 BM25-ordered candidates, expanded base | 113 / 122–124 | 197–201 | 47–48 | 19–21 |

Two functions are mixed in those rows. One is **ordering**: a useful fact is among the first twenty BM25 candidates for
most prompts, and the judge puts it first. The other is **abstention**: the words without a gate serve 128 noise facts,
the judge 21, and the note's own reading was that "what Jev brings is mostly the refusal to serve what does not fit".
The lexical gate cannot do that job alone: a coverage of 0.8 or more, the only lexical signal strong enough to skip the
judge without loss, occurs on 5 % of prompts ([judge-gate](judge-gate-2026-10.md)). Any local replacement is judged on
both functions, and the second is the harder one.

What the judge does not do. It is not stable: the served pair changed on 5–7 of 12 live prompts between identical runs,
from the fixed threshold and from gaps wider than its jitter ([judge-cut](judge-cut-2026-10.md)). It scores relevance,
not freshness, so a stale fact can win over the current one ([recall-misses](recall-misses-2026-10.md)). And whether it
changes the agent's *answers* is not measured: the end-to-end run of 40 prompts scored memcue with the words alone at
0.97 against 0.88 for file memory and 0.82 without memory, with a confidence interval that crosses zero
([e2e-files](e2e-files-2026-10.md)); the run in progress adds a judge arm to the same harness. That arm decides the
stakes of this note. If the judge does not beat the words end to end with Claude as the working model, the hot-path
judge is already optional for Claude Code, and a local ranker's job is to cut noise rather than to rescue prompts. The
literature says the harm side is where to look: on real repository tasks eleven of twelve solver-and-memory pairings
scored at or below the memory-off baseline[^vibemem], MemDev-Bench reports Harm of 4.8–9.9 % next to Rescue of
9.9–13.2 %[^memdev], and REM finds that models strong enough to solve a task unaided lose 2–9 points from *any* memory
condition[^rem].

## 2. Where a local decision can come from

### 2.1 The working model through the hooks: what is possible

The working model's own call is the one that costs nothing extra, so the first question is whether a hook can make it
decide. The Claude Code docs settle most of it[^cc-hooks]:

- A command hook on `UserPromptSubmit`, `SessionStart`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Stop` and
  `SubagentStop` can return `additionalContext`; Claude Code "wraps the string in a system reminder and inserts it into
  the conversation at the point where the hook fired". `PostToolUse` places it next to the tool result. `PreToolUse`
  is documented inconsistently (the decision table lists no context field) and would need a test.
- A `prompt` hook sends the hook input to "the model Claude Code uses for background functionality" and gets back only
  `{ok, reason, impossible}`; there is no context field, so a prompt hook cannot rerank or inject. An `agent` hook is
  marked experimental, runs up to 50 turns with Read, Grep and Glob, and returns the same two fields. The docs do not
  name the background model or say how its calls are billed[^cc-costs].
- There is no documented way for a hook to ask the live session's model a question synchronously. A `Stop` hook's
  `additionalContext` makes the model continue its turn (at most eight continuations in a row), which is a way to make
  it *act* at turn end, not a cheap query.
- Hooks are synchronous; a slow `UserPromptSubmit` hook blocks the prompt; its default timeout is 30 s.

So the working model can decide in two ways only: at read time, by being shown candidates and choosing (section 2.4);
or at write time, in the extraction call that already happens (section 3). Caching does not change the picture: the
docs describe three cache layers — system prompt, project context (CLAUDE.md, auto memory, unscoped rules), then the
conversation — and hook output lands in the conversation, which "leaves the system prompt and project context
cached"[^cc-cache]. The session card sits in the conversation prefix and is cached for the session; anything added on
every prompt is paid as cached input on every later turn.

### 2.2 A feature ranker over the words' candidates

The cheapest local reader is not a neural network. The words produce twenty candidates with a dozen numbers each, and a
gradient-boosted tree model over those numbers can order them and learn a threshold. `dmitryikh/leaves` (MIT) loads
LightGBM and XGBoost models into pure Go and predicts a row in about 50 µs on its README benchmark[^leaves], so the
reader stays inside the static binary the storage note defended ([storage](storage-2026-10.md)). Training happens in
Python, once, offline; the binary ships a JSON model.

What the features would be, all available from `recall -explain` or a few lines of store code: the BM25 score and rank;
coverage and the number of matched terms; which fields matched (content, keywords, `queries`) and whether a ticket key
did; the fact's length, kind, scope and age; whether its scope or origin matches the session's repository and host (today
only a tie-break); whether the session already got it and whether a past session used it (section 3). The listwise
context matters for abstention and costs nothing: the score and coverage relative to the best candidate of the list,
the gap between the first and second candidate, the number of candidates, the prompt's length and number of known
terms.

What the labels would be. memcue already holds 3,377 pairs with Jev probabilities and Opus and DeepSeek usefulness
labels ([main-judge](main-judge-2026-10.md)), 192 and 238 Opus-labelled pairs from the Hermes base
([threshold](threshold-2026-10.md), [listwise](listwise-2026-10.md)), and Jev scores for every live prompt in
`recall.log`. Labels are the cheap part: Jev scores 180 prompts for $0.017, so ten thousand prompts cost about a dollar.
Prompts are the scarce part, and they exist: `recall.log` keeps them with `MEMCUE_RECALL_LOG=1`, and the 636 transcripts
the gold set was drawn from hold every user prompt of those sessions. `judgecand` already writes the twenty candidates a
prompt would get on a base copy, `judge_ab.py` scores them, and `judge_ab_report.py` compares any reader's output on the
same labels, so the whole loop exists.

What the literature says to expect. A LambdaMART trained on about 10,000 pairs of a web ranking set kept 89 % of its
full-data nDCG, and shallower, more regularised trees lost less[^seznam]; the standard distillation budgets are
10,000–20,000 queries ([^rankdistillm], [^twolar]), so a few thousand real prompts are at the low end of what works for
ordering. Abstention is the weaker side. Adaptive-RAG trained a 770M classifier to decide whether retrieval is needed
and reached 30.5 % accuracy on the "no retrieval" class[^adaptiverag]; SkillApt's global logistic regression on hashed
features and a few hundred labels reached 83.8 % where a per-skill neighbour rule reached 91.3 %, and the paper's
lesson is that "relevance is not applicability"[^skillapt]. The threshold itself should be calibrated, not picked:
Platt scaling for small samples, isotonic only above about a thousand calibration points[^sklearn]. And a tree model
cannot read; a prompt that shares no rare word with its fact stays a miss, as it is today (5 of 47 misses on the gold
set, [recall-misses](recall-misses-2026-10.md)).

Two things a feature ranker gives that the judge does not: it is deterministic, so the served pair stops flipping
between runs, and it sees signals the judge cannot — the session's repository and host, the fact's age, whether it was
used before. Whether that is enough to reach the judge's 113 useful prompts is unknown, and it is the cheapest number
in this note to get: no new runtime, days of work, the existing scripts.

### 2.3 A local cross-encoder

A cross-encoder reads the prompt and a fact together and scores the pair, which is the judge's question asked of a small
model. Two sub-options: a published multilingual reranker as it is, or one fine-tuned on the judge's own answers.

| Model | Params | Russian | License | Note |
|---|---|---|---|---|
| `cross-encoder/mmarco-mMiniLMv2-L12-H384-v1`[^mminilm] | 0.1B | yes: mMARCO is MS MARCO translated into 13 languages, Russian among them[^mmarco] | Apache-2.0 | 12 layers, hidden 384; the smallest option with Russian in its training data |
| `Alibaba-NLP/gte-multilingual-reranker-base`[^gte] | 306M | "over 70 languages", Russian not named | Apache-2.0 | 8k context; custom architecture, no official ONNX export |
| `BAAI/bge-reranker-v2-m3`[^bge] | 0.6B | multilingual, Russian not named | Apache-2.0 | GGUF Q4_K_M 438 MB to Q8_0 636 MB[^bge-gguf]; the rerank example of the llama.cpp server |
| `Qwen/Qwen3-Reranker-0.6B`[^qwen3r] | 0.6B | "100+ languages", Russian not named | Apache-2.0 | scores P("yes") from the yes/no logits — natively the question memcue asks; GGUF Q8_0 639 MB[^qwen3-gguf] |
| `jinaai/jina-reranker-v2-base-multilingual`, `-v3`[^jina] | 278M, 0.6B | multilingual | CC-BY-NC-4.0 | out: non-commercial |
| MemReranker-0.6B[^memreranker] | 0.6B | not stated | — | distilled for memory retrieval; only the 4B weights are linked |

Latency on a CPU is the open number. The one careful measurement found is Qdrant's benchmark on a MacBook M4 Max with
14 cores, about 30 pairs per request, ONNX Runtime pinned to the CPU provider: `ms-marco-MiniLM-L-6-v2` at p99 36–51 ms
across three libraries and p50 29 ms, `jina-reranker-v2-base-multilingual` (278M) at p50 131–191 ms, ONNX session
creation 33–38 ms, and CoreML slower than the CPU provider (p50 213 ms against 29)[^qdrant]. A 12-layer MiniLM has twice
the layers of the L6 model, so twenty pairs of a prompt and a 300-character fact should take on the order of 60–150 ms on
Apple Silicon, with the session creation on top because the hook is a fresh process. That is an extrapolation; nothing
was found for the mMiniLMv2 or gte models on a CPU, and nothing at all for a small Linux VPS.

Quality as published is below a large judge and the scores are not probabilities. On TREC DL19 a MiniLM-L12
cross-encoder scores 70.8 nDCG@10 against 75.6 for RankGPT-4, with the gap wider on other sets, and the same survey finds
lightweight models "achieve strong trade-offs between accuracy and efficiency"[^llmrerank]; Déjean et al. conclude
that "traditional cross-encoders remain very competitive"[^dejean]. MemReranker's motivation is the threshold problem
exactly: generic rerankers' scores "cluster near 0.00, making it virtually impossible for production systems to set
effective cutoff thresholds"[^memreranker]. An off-the-shelf model therefore needs the same calibration as the tree
model, and will likely sit below Jev on the gold set; it should be measured as a feature next to the words, not only as
a replacement.

Distillation closes the gap, and the recipe is simple. Rank-DistiLLM distilled a listwise LLM into an ELECTRA
cross-encoder from the top-50 candidates of 10,000 MS MARCO queries with a pairwise RankNet loss, found a listwise loss
"unnecessary for distillation from LLMs", and its Large two-stage model beat RankGPT-4 and RankZephyr on three of four
TREC DL columns (0.733 and 0.727 nDCG@10 on DL19 and DL20 over BM25 against 0.713 for RankGPT-4)[^rankdistillm]. TWOLAR
distilled gpt-3.5 into Flan-T5 from 20,000 queries for $212 of API calls and found the depth per query mattered more
than the number of queries: 10 documents per query cost four nDCG points against 30[^twolar]. InPars kept about 10,000
positive pairs *(unverified)*[^inpars]. The Sentence-Transformers cross-encoder trainer takes a float label per pair,
so the judge's probability can be the target directly[^sbert]. MemReranker, the one paper that distils for agent
memory, fine-tuned Qwen3-Reranker-0.6B on about a million general pairs plus 50,000 dialogue pairs from GPT and Qwen
pairwise judgments and tied GPT-4o-mini on LoCoMo (MAP 0.7150 against 0.7151) at 247 ms average latency on unstated
hardware[^memreranker]. That is an order of magnitude more data than memcue can label with real prompts, but memcue's
task is narrower: one user's facts, one user's way of asking, and twenty candidates the words already chose.

The runtime is the real obstacle, and it is the same one that decided against DuckDB. memcue ships one static binary per
platform with `CGO_ENABLED=0`. `hugot` lists a `crossEncoder` pipeline and three backends: a native Go backend that "does
not require any cgo dependencies" and "is designed for simpler workloads, environments that disallow cgo, and for
smaller models such as all-MiniLM-L6-v2"; ONNX Runtime behind a build tag, "currently the fastest backend", which loads
`libonnxruntime.so`; and XLA. Its README says "the library is only built/tested on amd64-linux currently" and never
mentions macOS or arm64[^hugot]. `onnxruntime_go` needs cgo and the shared library beside the binary[^ortgo]. A
sidecar is the other shape: `llama-server` serves `/v1/rerank` for an encoder such as bge-reranker-v2-m3 (which "must fit
the whole prompt within `--ubatch-size`") and for decoder rerankers[^llamaserver]; its resident memory is roughly the
GGUF size, and Qwen3-Reranker support in the mainline server is reported both ways *(unverified)*. Ollama has no rerank
API: the issue asking for one was closed without a maintainer answer[^ollama]. A sidecar is the Ollama embedder again —
1.3 GB resident and 1.6 s cold ([one-model](one-model-2026-10.md)) — only smaller.

Static embeddings do not shorten the path. Model2Vec's `potion-multilingual-128M` is 128M parameters at 256 dimensions,
distilled from bge-m3[^potion]; the README claims inference "up to 500 times faster on CPU" and shows that it is a
token lookup and a mean[^model2vec], but no Go implementation exists, the published matrix is about half a gigabyte as
float32, and no source was found that uses static similarity as a reranking feature, let alone for abstention. The
gold set already measured dense similarity alone at 21 useful top-1 prompts of 120 against 44 for the words
([gold-set](gold-set-2026-10.md)).

### 2.4 The working model chooses from stubs

The design that uses the working model's judgment at read time without an extra call is the one the agent products
converge on: show the model a short index, let it fetch what it decides it needs. Claude Code's own auto memory loads the
first 200 lines or 25 KB of `MEMORY.md` and leaves topic files to be read on demand[^cc-memory]; skill descriptions
stay in context, cut to 1,536 characters each, and the full skill loads when the model invokes it[^cc-skills]; Cursor's
"Agent Requested" rules put only the descriptions in the system prompt with an instruction to call `fetch_rules`[^cursor-fetch];
Windsurf's `model_decision` rules show "only the `description`" and Cascade "reads the full rule file when it decides the
description is relevant"[^windsurf]; Codex injects a summary and tells the model to grep `MEMORY.md` within "4–6 search
steps" and to cite what it used[^codex]. For memcue the shape would be: the `UserPromptSubmit` hook injects the first few
candidates as one-line stubs (id, scope, the first eighty characters) with a line saying when to call `memcue show ID`,
and the skill does the rest.

The evidence on whether models take the offer is mixed and hinges on the model and the prompt. With Claude Opus 5.5,
coding agents given the raw history file and their own tools scored 97–99 % on a memory benchmark against 95 % for the
full context; with GPT-4o-mini the same agents scored 28–29 %, and "Claude Code almost never" opened the file
(one conversation, 152 questions, one run per cell)[^memorytax]. MemCog puts the decision in the working model's own
reasoning, "requiring no additional gating module", but only under a protocol in the system prompt: removing it drops
Recall@5 from 59.51 to 44.90 while ordinary QA is unchanged[^memcog]. APDMem finds that the decision to expand the right
stub is the bottleneck, with retrieval errors falling from 22.2 % to 10.6 % purely from a stronger controller[^apdmem].
Cursor staff answer "the agent sometimes ignores rules" with "Rules in context don't guarantee 100 % adherence"[^cursor-adherence],
and the Claude Code tracker has an open issue on skills that do not trigger[^cc-9716]. The one controlled measurement on
Claude Code is a blog post: 22 prompts that each map to one skill, Sonnet 4.5, two runs, skill invoked in 55 % and 50 % of
runs with no hook, 100 % with a `UserPromptSubmit` hook that forces an explicit evaluation step, and on a harder set with
five prompts that need no skill the forced step kept all five negatives[^spence]. Progressive disclosure has a size
rule: one small index level helps, and a long always-loaded list of per-item descriptions hurts, from 0.91 to 0.64 on one
task in a controlled study[^progdisc]. In MemDev-Bench the stub-then-read plugins had the lowest Harm of the systems
tested, 4.8–6.8 %[^memdev].

Two costs decide it. A pull is a tool round trip, several seconds against the judge's half a second, so on the prompts
where the model does fetch, the turn gets slower, not faster. And the stubs are paid on every prompt, as cached input on
every later turn. The design fits Claude Code with a strong model and a forced-evaluation line; it does not fit the
Hermes gateway, whose models are the ones that "almost never" look. It is worth one arm on the end-to-end harness, where
the question is whether the answers get better, not the main path.

### 2.5 Triggers from the agent's state

Every surveyed system that wants determinism keys knowledge on what the agent *touches*, not on what the user asked.
Claude Code loads a rule with a `paths` frontmatter "when Claude uses the Read, Write, or Edit tool on a matching file",
including a `cat` of a single file[^cc-memory]; Windsurf's `glob` rules fire "when Cascade reads or edits a file
matching"[^windsurf]; Kiro's steering files have `inclusion: fileMatch`[^kiro]; Continue adds a `regex` trigger on
file *content*, the only content-keyed trigger found[^continue]; Gemini CLI scans for `GEMINI.md` when a tool accesses a
directory[^gemini]; Copilot's instruction files carry `applyTo` globs[^copilot-instr]. The academic results point the
same way. REM injects a 270-token document at start and the costly implementation record only on a deterministic
struggle signal, "the first time the agent fails a validation", and evicts it after one exposure: 27.6 % against 25.3 %
for eager injection and 24.9 % without memory, with 12 % fewer tokens[^rem]. AutoGuide keys guidelines on the agent's
current context and beats ExpeL, which injects every guideline, by 20–25 points across three benchmarks[^autoguide].
Codex captures state on the write side and Copilot verifies a memory's code citations before using it, which made a
deliberately seeded false memory "self-heal"[^copilot-blog].

For memcue this is a second read channel next to the prompt-keyed one, and it needs no model at read time. A
`PostToolUse` or `PostToolUseFailure` hook with an `if` matcher such as `Edit(*.go)` or `Bash(glab *)`[^cc-hooks] runs a
`memcue trigger` command with the tool's input and response; the command matches them against **anchors** stored with
the facts (path globs, command prefixes, error signatures, host names) and injects a matching fact next to the tool
result, once per session. Matching an anchor against a path or a command line is a string comparison in microseconds;
matching an error signature against a tool response is a substring or FTS lookup. The facts that can carry an anchor are
the pitfalls and environment facts: a tool's non-obvious failure and its fix, a host's layout, a command's required
flag. Preferences and decisions have no anchor and stay with the card and the prompt channel. What memcue lacks is the
anchors themselves, which is a write-time question (section 3). The REM rule of evicting after one exposure is what
memcue already does by serving a fact once per session.

## 3. Write-time intelligence: paying once with the model that works

The way to use the working model's family without a call per prompt is to let it do the thinking when it is already
called: at extraction, in the background, on `claude -p` or the configured endpoint. memcue already does this once —
the `queries` a fact is stored with, which added 7–9 useful prompts for the judge and 1–4 for the words alone
([one-model](one-model-2026-10.md)), and which LongMemEval found to be the write-time move that pays
([other-memories](other-memories-2026-10.md)). Three more things the same call can write:

- **Anchors** for the state channel: for a fact about a tool, command, path, host or error, the path glob, command
  prefix or error signature that should recall it. The dream fills anchors for older facts the way it fills queries.
  Copilot's citations are the precedent[^copilot-blog].
- **Usage marks.** The transcript the extraction reads contains the `memcue: stored facts` blocks with the ids the
  session was served. Asking the extraction to list the ids whose fact the session used — a value quoted, a command
  run, a rule followed — costs no extra call. Codex ranks its memories by `usage_count` and prunes by
  `max_unused_days`[^codex]; Copilot deletes a memory "unused after 28 days" and resets the timer on a validated
  use[^copilot-mem]; the Hermes provider already keeps an identifier-copy proxy in `usage.log`. The safe uses are
  retention (a dream proposes retiring facts never used in N days, card facts exempt) and a logged feature for the ranker
  of section 2.2. Feeding use back into ranking without care builds a loop: served facts get used, used facts get served
  (Joachims' position bias[^joachims] and Chaney's "pernicious feedback loop"[^chaney] are the same warning), so the log
  should keep the rank a fact was served at, and the ranker should be trained with it. A Memory-Worth counter of
  success and failure co-occurrence[^whentoforget] needs an outcome signal memcue does not have.
- **Transitions** ("moved from X to Y because Z", mem0 v3's rule), so the new fact carries the words that make the old
  one lose; still unmeasured ([other-memories](other-memories-2026-10.md), try 6).

Everything above is a prompt change in `internal/extract` and a column or two in the store; none of it touches the hook.

## 4. An architecture

Three channels on the read path, none of them calling a model:

```
A  session start      card: global rules, user-stated facts, the repository's decisions   deterministic, budgeted,
                      (unchanged; cached in the session prefix)                            re-sent after compaction

B  each prompt        FTS5 -> 20 candidates in BM25 order
                        -> local ranker (2.2: features; 2.3: distilled cross-encoder)
                        -> calibrated abstain threshold -> at most two facts              tens of ms, deterministic

C  each tool call     PostToolUse / PostToolUseFailure with `if` matchers
                        -> anchors (path, command, error, host) -> the matching fact       microseconds, once a session

W  after a session    extraction writes facts + queries + anchors + usage marks;           the model that works,
                      vet and dream as today; dream fills anchors and expires by disuse     in the background
```

A is the "always, within a budget" the owner asked for, and it exists. The "repository's facts when they are needed"
splits in two: the decisions ride in A, and the pitfalls and environment facts reach the agent through C when it
touches what they are about, and through B when the prompt names it. B keeps the reader abstraction of
`internal/rerank` (`MEMCUE_RERANK` gains a `local` value next to `judge`, `listwise` and `off`), so Jev stays available
as an opt-in reader and, more usefully, as the offline labeller that trains the local one. Triage stays on Jev as
today, opt-in and off; for a user who does not turn it on, the second provider disappears.

The two hosts differ in what they need from B. In Claude Code the working model is strong, the judge's end-to-end value
is the open measurement, and even a modest local ranker removes a second of latency on a quarter of the prompts. In the
Hermes gateway the models are weaker, which is where the literature finds curated memory to help and noise to hurt
most[^memorytax][^rem], and where the hook pays a TLS handshake per process; there the local ranker's quality matters,
and so does its determinism.

## 5. What to measure, in order

1. **The judge arm of the end-to-end run** (in progress). If `judge` does not beat `memcue` (the words alone) on the 40
   prompts, the rerank is optional in Claude Code and step 2 is about noise; if it does, step 2 has a target.
2. **The feature ranker offline.** `judgecand` candidates for the 171-prompt set, the existing Jev scores and Opus
   labels, a LightGBM model with the features of section 2.2 trained on prompts disjoint from the evaluation prompts,
   Platt-calibrated, written as a reader JSONL for `judge_ab_report.py`. Report useful prompts, noise and, separately,
   the precision of "served nothing". A tree model is deterministic, so the two-run spread of the judge notes becomes
   zero by construction.
3. **More labels, same loop.** Prompts from `recall.log` and the transcripts, candidates on a base snapshot, Jev scores
   as teacher labels; retrain; plot useful prompts against the number of training prompts, which no surveyed paper
   gives below 10,000 queries.
4. **The cross-encoder's two numbers.** Latency of `mmarco-mMiniLMv2-L12-H384-v1` as int8 ONNX for twenty pairs on the
   `hugot` Go backend on darwin/arm64 and on the VPS, session creation included; then its gold-set score raw, calibrated,
   and after fine-tuning on the step-3 labels with a float BCE target. This settles whether the model earns a cgo
   build or a sidecar.
5. **The state channel's coverage.** How many active facts can carry an anchor; replay the transcripts of the gold set
   and count how often an anchor fires and whether the labeller calls the fact useful for that session. This is the
   harm metric of [other-memories](other-memories-2026-10.md), try 1, applied to C.
6. **Stubs as an end-to-end arm**, only in Claude Code: five stubs and a forced-evaluation line, scored by the e2e
   harness against the words alone.

## Outcome

The six steps of section 5 were measured on 2026-10-23 (#316–#320). Total spend about $58: $7.18 of Opus labels for the
local readers, $3.08 for the anchors, $31.82 of stubs runs and $15.91 of grading, about $0.2 of Jev, and 367k input
and 135k output tokens of the main model for the anchors.

| step | result | note |
|---|---|---|
| 1. judge end to end | judge 1.06–1.07, words 0.95–0.97; only judge against none outside the noise | [e2e-files](e2e-files-2026-10.md) |
| 2. feature ranker | 49–59 useful prompts top-1 against Jev's 92–94; at the words' level of useful prompts, half their noise (32 against 66) | [local-readers](local-readers-2026-10.md) |
| 3. more labels | 1442 more prompts change the trees by a few prompts: the features are the limit | [local-readers](local-readers-2026-10.md) |
| 4. cross-encoder | published: 42 top-1, below the words; fine-tuned on Jev: 61; ONNX Runtime 155 ms + 324 ms cold, the cgo-free Go backend 3–9 s | [local-readers](local-readers-2026-10.md) |
| 5. state channel | 63 % of facts get an anchor, a turn gets a median of 8–48 fires, 11 % of them useful | [anchors](anchors-2026-10.md) |
| 6. stubs | 0.99, between the words and the judge, inside the noise; the agent reads 2.5 of 5 stubs | [e2e-files](e2e-files-2026-10.md) |

**The direction chosen.** No local reader replaces Jev, and Jev stays the reader wherever its key is set: it is the
only reader with a gain end to end, it costs under a cent a session, and a fresh local model would take as long. The
dependency is reduced instead of removed:

1. **The tree ranker becomes the local reader** (`MEMCUE_RERANK=local`, `leaves` in pure Go, the model shipped as
   JSON) and replaces both the words' gate and the listwise fallback when the judge is off, unconfigured or paused.
   Without a judge key memcue then makes no model call on the read path, and serves half the noise the words serve
   today. This is the next implementation issue.
2. **Prompts keep being collected** (`MEMCUE_RECALL_LOG=1`) and Jev keeps scoring them offline, so the fine-tuned
   cross-encoder can be measured again once the pool is several times its 1442 prompts; it is the one local reader that
   gains from more data.
3. **Not pursued**: anchors written by the model, the published cross-encoder, the cgo-free cross-encoder runtime, and
   stubs as a default.

## Contradictions and open questions

- **How far a small model is behind a large judge.** On MS MARCO-trained models the gap is 2–5 nDCG points on TREC DL and
  larger on other sets[^llmrerank]; trained on the judge's own answers the gap closes at 10,000 queries[^rankdistillm];
  MemReranker needed about a million pairs to tie GPT-4o-mini on memory data[^memreranker]. Which regime a base of a
  few thousand prompts falls in is the question of step 3.
- **Push or pull.** Anthropic's memory tool and Letta argue for agent-managed, just-in-time memory, and with a frontier
  model pull-only scores highest on MemoryTax[^memorytax]; MemCog and the Proactive Memory Agent show agents do not pull
  without an explicit protocol and that selective push beats always-on[^memcog][^proactive]. Both are right about
  different models.
- **Relevance against applicability.** SkillApt, MemDev-Bench and the admission study all show topically relevant
  memories doing harm, and a query-conditioned relevance gate did not remove it[^skillapt][^memdev][^admission]. Neither
  Jev nor a local ranker answers "worth injecting now"; only an end-to-end harm measurement sees that.
- **Usage as a signal.** Codex ranks by it[^codex]; the unbiased learning-to-rank literature says logged use is biased
  by what was shown[^joachims][^chaney]. The two agree once the rank is logged with the use.
- **Whether a strong working model needs a judge at all.** REM and MemoryTax find strong models gain little or lose from
  memory[^rem][^memorytax]; memcue's own 30-of-30 end-to-end run and the 0.97 score say the words alone help. The judge
  arm of the current run is the first direct test.

## Insufficient evidence

- CPU latency of `mmarco-mMiniLMv2-L12-H384-v1`, `gte-multilingual-reranker-base` or `Qwen3-Reranker-0.6B` on Apple
  Silicon or a small Linux VPS: no source; only the Qdrant benchmark of two other models.
- `hugot` on darwin/arm64: the README names amd64-linux as the only built and tested platform.
- Qwen3-Reranker on the mainline `llama-server` rerank endpoint: reported both working and needing a fork.
- Any paper comparing LLM judges with cross-encoders on short-fact memory retrieval in Russian: none found.
- Billing of `prompt` and `agent` hooks: not in the docs; moot, since they cannot inject context.
- Cursor's rule documentation: cursor.com was unreachable from the surveying host, so its wording rests on snippets and
  forum quotes.

## Limits

- No measurement. Every expected number above is a transfer from passage ranking or from another agent's benchmark to
  short bilingual facts of one user; the transfer is the hypothesis.
- Fifteen of the academic sources are 2026 preprints read once; their numbers were taken as printed, and two of them
  (MemCog, MemReranker) have internal inconsistencies noted by the second reader.
- The hook facts were read through a fetch tool's rendering of the docs, not the raw pages; exact wording should be
  checked against the source before it is relied on verbatim.
- The skill-activation measurement is one blog post with 22 prompts and two runs.

## Sources

[^cc-hooks]: Claude Code docs, *Hooks reference*. https://code.claude.com/docs/en/hooks
[^cc-memory]: Claude Code docs, *How Claude remembers your project*. https://code.claude.com/docs/en/memory
[^cc-skills]: Claude Code docs, *Skills*. https://code.claude.com/docs/en/skills
[^cc-cache]: Claude Code docs, *Prompt caching*. https://code.claude.com/docs/en/prompt-caching
[^cc-costs]: Claude Code docs, *Manage costs effectively*. https://code.claude.com/docs/en/costs
[^hugot]: knights-analytics/hugot, README. https://github.com/knights-analytics/hugot
[^ortgo]: yalue/onnxruntime_go, README. https://github.com/yalue/onnxruntime_go
[^llamaserver]: ggml-org/llama.cpp, `tools/server/README.md`. https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
[^qwen3r]: Qwen/Qwen3-Reranker-0.6B, model card. https://huggingface.co/Qwen/Qwen3-Reranker-0.6B
[^qwen3-gguf]: ggml-org/Qwen3-Reranker-0.6B-Q8_0-GGUF. https://huggingface.co/ggml-org/Qwen3-Reranker-0.6B-Q8_0-GGUF
[^bge-gguf]: gpustack/bge-reranker-v2-m3-GGUF. https://huggingface.co/gpustack/bge-reranker-v2-m3-GGUF
[^ollama]: ollama/ollama#4510, "support re-rank models". https://github.com/ollama/ollama/issues/4510
[^mmarco]: unicamp-dl/mmarco, dataset card. https://huggingface.co/datasets/unicamp-dl/mmarco
[^mminilm]: cross-encoder/mmarco-mMiniLMv2-L12-H384-v1, model card. https://huggingface.co/cross-encoder/mmarco-mMiniLMv2-L12-H384-v1
[^gte]: Alibaba-NLP/gte-multilingual-reranker-base, model card. https://huggingface.co/Alibaba-NLP/gte-multilingual-reranker-base
[^bge]: BAAI/bge-reranker-v2-m3, model card. https://huggingface.co/BAAI/bge-reranker-v2-m3
[^jina]: jinaai/jina-reranker-v2-base-multilingual and jina-reranker-v3, model cards. https://huggingface.co/jinaai/jina-reranker-v2-base-multilingual
[^qdrant]: C. Bertelli, *Oxidizing Cross-Encoders*, Qdrant blog, 2026-09-25. https://qdrant.tech/blog/oxidizing-cross-encoders/
[^model2vec]: MinishLab/model2vec, README. https://github.com/MinishLab/model2vec
[^potion]: minishlab/potion-multilingual-128M, model card. https://huggingface.co/minishlab/potion-multilingual-128M
[^memreranker]: C. Li et al., *MemReranker*, arXiv 2605.06132, 2026. https://arxiv.org/abs/2605.06132
[^rankdistillm]: F. Schlatt et al., *Rank-DistiLLM: Closing the Effectiveness Gap Between Cross-Encoders and LLMs for Passage Re-Ranking*, ECIR 2025, arXiv 2405.07920. https://arxiv.org/abs/2405.07920
[^twolar]: D. Baldelli et al., *TWOLAR: a TWO-step LLM-Augmented distillation method for passage Reranking*, ECIR 2024, arXiv 2403.17759. https://arxiv.org/abs/2403.17759
[^inpars]: L. Bonifacio et al., *InPars*, arXiv 2202.05144, 2022 (search snippet only). https://arxiv.org/abs/2202.05144
[^sbert]: Sentence-Transformers docs, *Cross-Encoder loss overview*. https://sbert.net/docs/cross_encoder/loss_overview.html
[^seznam]: M. Modrý, M. Ferov, *Enhancing LambdaMART Using Oblivious Trees*, arXiv 1609.05610, 2016. https://arxiv.org/abs/1609.05610
[^leaves]: dmitryikh/leaves, README. https://github.com/dmitryikh/leaves
[^sklearn]: scikit-learn docs, *Probability calibration*. https://scikit-learn.org/stable/modules/calibration.html
[^adaptiverag]: S. Jeong et al., *Adaptive-RAG*, arXiv 2403.14403, 2024. https://arxiv.org/abs/2403.14403
[^copilot-mem]: GitHub Docs, *Copilot Memory*. https://docs.github.com/en/copilot/concepts/agents/copilot-memory
[^copilot-blog]: T. Gazit, *Building an agentic memory system for GitHub Copilot*, GitHub blog, 2026-01. https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/
[^copilot-instr]: GitHub Docs, *Adding repository custom instructions*. https://docs.github.com/en/copilot/how-tos/configure-custom-instructions/add-repository-instructions
[^codex]: openai/codex source: `codex-rs/memories/README.md`, `codex-rs/ext/memories/templates/memories/read_path.md`, `read_path_v2.md`, `codex-rs/config/defaults.toml`. https://github.com/openai/codex/tree/main/codex-rs/memories
[^whentoforget]: *When to Forget*, arXiv 2604.12007, 2026. https://arxiv.org/abs/2604.12007
[^joachims]: T. Joachims et al., *Unbiased Learning-to-Rank with Biased Feedback*, arXiv 1608.04468. https://arxiv.org/abs/1608.04468
[^chaney]: A. Chaney, B. Stewart, B. Engelhardt, *How Algorithmic Confounding in Recommendation Systems Increases Homogeneity*, arXiv 1710.11214. https://arxiv.org/abs/1710.11214
[^llmrerank]: *How Good are LLM-based Rerankers? An Empirical Analysis*, arXiv 2508.16757, 2025. https://arxiv.org/abs/2508.16757
[^dejean]: H. Déjean et al., *A Thorough Comparison of Cross-Encoders and LLMs for Reranking SPLADE*, arXiv 2403.10407, 2024. https://arxiv.org/abs/2403.10407
[^proactive]: *Remember When It Matters: Proactive Memory Agent for Long-Horizon Agents*, arXiv 2607.08716, 2026. https://arxiv.org/abs/2607.08716
[^memcog]: *MemCog*, arXiv 2605.28046, 2026. https://arxiv.org/abs/2605.28046
[^vibemem]: *VibeMemBench*, arXiv 2609.23570, 2026. https://arxiv.org/abs/2609.23570
[^memdev]: *MemDev-Bench: Does Memory Help Code Agents?*, arXiv 2610.27524, 2026. https://arxiv.org/abs/2610.27524
[^rem]: *REM*, arXiv 2610.27377, 2026. https://arxiv.org/abs/2610.27377
[^skillapt]: *SkillApt*, arXiv 2609.26863, 2026. https://arxiv.org/abs/2609.26863
[^progdisc]: *Is Progressive Disclosure All You Need?*, arXiv 2607.17598, 2026. https://arxiv.org/abs/2607.17598
[^apdmem]: *APDMem*, arXiv 2610.02472, 2026. https://arxiv.org/abs/2610.02472
[^memorytax]: Z. Peng, *MemoryTax*, arXiv 2610.29823, 2026. https://arxiv.org/abs/2610.29823
[^autoguide]: Y. Fu et al., *AutoGuide*, NeurIPS 2024, arXiv 2403.08978. https://arxiv.org/abs/2403.08978
[^admission]: *Admission versus presentation of memories*, arXiv 2610.11188, 2026. https://arxiv.org/abs/2610.11188
[^cursor-fetch]: Cursor forum, "Rules not being applied automatically", 2025-02 (quotes the system prompt with `fetch_rules`). https://forum.cursor.com/t/rules-not-being-applied-automatically/50113
[^cursor-adherence]: Cursor forum, "Cursor agent not following rules", staff replies of 2026-01. https://forum.cursor.com/t/cursor-agent-not-following-rules/149542
[^windsurf]: Windsurf/Cascade docs, *Memories & Rules*. https://docs.devin.ai/desktop/cascade/memories
[^kiro]: Kiro docs, *Steering*. https://kiro.dev/docs/steering/
[^continue]: Continue docs, *Rules*. https://docs.continue.dev/customize/deep-dives/rules
[^gemini]: Gemini CLI docs, *GEMINI.md*. https://geminicli.com/docs/cli/gemini-md/
[^spence]: S. Spence, *Measuring Claude Code Skill Activation With Sandboxed Evals*, 2026-02-08. https://scottspence.com/posts/measuring-claude-code-skill-activation-with-sandboxed-evals
[^cc-9716]: anthropics/claude-code#9716, skills not triggering. https://github.com/anthropics/claude-code/issues/9716

## Quality metrics

| Metric | Value |
|---|---|
| Sources cited | 57 |
| Academic | 24 |
| Official docs and source code | 26 |
| Industry blogs | 3 |
| Forums and issue trackers | 4 |
| Verified by a second reading | hugot, MemReranker, Rank-DistiLLM, the skill-activation post, mMARCO, the Qdrant benchmark, MemCog, MemoryTax |
| Marked unverified | InPars figures, Qwen3-Reranker in mainline llama.cpp, Cursor docs wording |
| Counter-arguments searched | yes (section "Contradictions") |
| Research rounds | 1 survey round, 1 verification round |
| Questions left without data | 6 (section "Insufficient evidence") |
