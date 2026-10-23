# How other agent memories work, and what memcue should take, October 2026

Status: **literature and source review of 2026-10-23, no measurement.** The question was how other long-term memory
systems for LLM agents are built — coding-agent features, general frameworks, academic work — and which of their
techniques memcue should ignore and which it should try. Every "try" below is a hypothesis for a measured issue, in
the same shape as the other notes in this directory; nothing here changes a default.

## Method

Five parallel searches, one per sub-question: coding-agent memory features, memory frameworks and their lifecycle,
academic work and benchmarks, the read path and memory safety, criticism and evaluation. About 40 pages and papers
were read in full or through their source code; the rest was taken from abstracts or search results and is marked
*(abstract)* or *(secondary)* where it matters. Key numbers that rested on one reader were checked a second time
against the arXiv abstract pages. memcue's own code was checked for each technique before it was listed as missing.

What memcue is, for the comparison: atomic facts in one SQLite file synced through git; a `UserPromptSubmit` hook
with FTS5 (Snowball EN/RU) and an IDF coverage gate, the first 20 BM25 candidates reranked by Jev, at most two facts
injected with their scope and date; a session-start card of rules and decisions; extraction of candidate facts at
compaction and session end; `vet` (rules and a model) and a review page for candidates; `dream` for duplicates and
outdated facts. Already measured and rejected here: embeddings ([vectors.md](../vectors.md),
[one-model-2026-10.md](one-model-2026-10.md)), whole-base judging ([gold-set-2026-10.md](gold-set-2026-10.md)),
other rerankers ([main-judge-2026-10.md](main-judge-2026-10.md), [luna-decisions-2026-10.md](luna-decisions-2026-10.md)).

## 1. Does memory help at all

The strongest finding of the review is not a technique but a warning: independent 2026 benchmarks on coding tasks
find that most memory systems do not beat a memory-off run.

- VibeMemBench (real repository tasks; Mem0, SimpleMem, MemoryOS, A-MEM): "eleven of twelve solver and system pairings
  fail to exceed the matched memory-off baseline", while injecting verified experience directly "raises observed task
  resolution on four of them by 1.1 to 4.5 percentage points while lowering agent steps on all five".[^vibemem] In the
  body, 69.3 % of failures are form degradation and 1.3 % are ranking misses; irrelevant text costs 0.7–3.4 points.
- MemDev-Bench: harmful runs "mainly stem from agents reusing memory without reassessing its applicability", and
  "unrelated history tends to increase such failures".[^memdev] The body reports Rescue 9.9–13.2 % against Harm
  4.8–9.9 % *(read by one reader, not in the abstract)*.
- Context files: on SWE-bench Lite and AGENTbench, "providing context files does not generally improve task success
  rates, while increasing inference cost by over 20% on average".[^agentsmd] In the body, LLM-generated files cost
  0.5–2 points and developer-written ones gain about 4 points on AGENTbench, except with Claude Code. A second
  ablation on Claude Code and Codex found no measurable change in correctness within ±10–15 points *(abstract)*.[^twoagent]
- Classic distraction results agree: irrelevant context lowers accuracy sharply[^gsmic], and in LongMemEval 40–50 % of
  errors are correct retrieval followed by a wrong answer.[^longmemeval]
- The selective side wins where it was tested: a memory agent that decides when to speak "outperforms passive bank
  exposure, always-on injection, advisor-only guidance, and general retrieval", +8.3 points on Terminal-Bench 2.0 and
  +6.8 on τ²-Bench.[^proactive]

Practitioners report the same failure in words: memories from unrelated contexts pollute answers ("Every time I ask
Claude Kubernetes question, it relates it to that one time I was deploying Kubernetes on VMs"), time-bound trivia gets
stored, and several people turned memory off.[^hn-chatgpt][^hn-claude] Claude Code's own tracker has "auto-memory is
actively harmful"[^cc-23544] and "months-old MEMORY.md silently loads into every session — no freshness indication, no
expiry".[^cc-85075] Cursor removed Memories in 2.1 and told users to move them into rules.[^cursor]

**For memcue** this supports what is already there — a gate that gives nothing to most prompts, a cap of two, the
"data, not instructions" frame — and says the metric that matters is harm, not hit rate. The gold set counts prompts
with a useful first fact; it does not count prompts a served fact made worse.

## 2. What the others do

### Coding agents

| System | Written by | Stored as | Read how | Staleness |
|---|---|---|---|---|
| GitHub Copilot Memory[^copilot-blog][^copilot-docs] | agent tool call when a fact has "actionable implications" | `subject`, `fact`, `citations` (`file:line`), `reason` | most recent facts of the repo in the prompt | citations re-read before use; "only used if it is successfully validated"; unused facts deleted after 28 days |
| Claude Code auto memory[^cc-memory] | Claude, from corrections; skips "anything it can derive from the codebase" | `MEMORY.md` index + topic files per project | first 200 lines / 25 KB always; topic files on demand | none; files stay until edited |
| Anthropic memory tool[^memtool] | the model, client-side file ops | `/memories` files | the API injects "ALWAYS VIEW YOUR MEMORY DIRECTORY BEFORE DOING ANYTHING ELSE" | advice: delete files not accessed for a long time |
| OpenAI Codex memories (source)[^codex] | background job on idle rollouts (≥ 6 h idle, ≤ 2 per start, only with ≥ 25 % rate limit left); secrets redacted | raw memories → consolidated `MEMORY.md`, `memory_summary.md`, skills, in a git baseline | summary (≤ 2 500 tokens) always; agent greps `MEMORY.md`; must end with `<oai-mem-citation>` | consolidation drops memories unused for 30 days, ranks by `usage_count`; threads with MCP or web search marked `polluted` |
| Windsurf / Cascade[^windsurf] | auto, or on request | local per workspace | "when it believes they're relevant" | none documented; docs advise rules or AGENTS.md for durable knowledge |
| Cursor[^cursor] | sidecar model *(secondary)* | — | — | feature removed in 2.1 |
| Devin Knowledge *(secondary)* | suggested from feedback, user approves | entries with a retrieval trigger | by trigger | — |
| Gemini CLI, Cline, Kiro, Continue, OpenHands *(secondary)* | explicit (`save_memory`) or file conventions | markdown files | always-on, glob or keyword triggers | none |
| claude-mem[^claudemem] | model on every tool call ("100+ times" a session) | SQLite + FTS5, optional Chroma | session-start context, search tools | none; users report a phrase-match FTS bug and observer costs of ~100 k tokens a turn[^claudemem-issues] |

### General frameworks

| System | Write path | Conflicts | Forgetting | Read path |
|---|---|---|---|---|
| mem0 paper (2025)[^mem0] | extract, then LLM chooses ADD/UPDATE/DELETE/NOOP against 10 similar memories | DELETE (graph variant marks invalid) | — | dense; graph variant +1.5 J, worse on single-hop, 3× search p95 |
| mem0 v3 OSS (2026)[^mem0-v3] | **ADD-only**, one LLM call: "the new fact is stored alongside the old one. Retrieval handles ranking" | links contradictions, records transitions ("switched from almond milk to oat milk") | `expiration_date`; no decay in OSS | semantic + BM25 + entity boost; no recency term in code |
| Zep / Graphiti[^zep][^graphiti] | entity and edge extraction; exact-text fast path, MinHash for names, LLM only for the rest | bi-temporal edges, `invalid_at` set, never deleted; prompt: "NEVER mark facts as contradicted unless the new fact explicitly makes the old fact false" | via invalidation | cosine + BM25 + BFS, RRF/MMR/cross-encoder |
| Letta[^letta] | "dreaming" subagent after N steps or at compaction, optional second review pass | — | — | memory is a git repo of markdown (MemFS); root files always in prompt; no vector index by default |
| LangMem[^langmem] | background manager with debounce; queries "formatted as hypothetical memories" | deletes off by default | — | dense |
| A-MEM[^amem] | keywords, tags, context per note; links to 5 neighbours; rewrites neighbours' tags and context, not content | — | — | dense |
| Hindsight[^hindsight] | narrative facts with event interval and mention time; entity summaries regenerated in background | — | — | semantic + BM25 + graph + temporal (rule-based date parsing first) → RRF → cross-encoder |
| MemoryOS[^memoryos] | short → mid → long term | — | heat score (visits, length, recency), LFU eviction | dense |
| Memobase[^memobase] | buffered, flushed at 1 024 tokens or 1 h idle, 3 LLM calls | — | — | profile by SQL, < 100 ms |
| Supermemory[^supermemory] | `updates` (with `isLatest`), `extends`, `derives` | history kept | "Episodes — Decays unless significant"; temporary facts expire | — |

The retrieval side (dense + BM25 fused, optional reranker) was surveyed in
[one-model-2026-10.md](one-model-2026-10.md) and is not repeated.

### Academic work

- **Key expansion at write time.** LongMemEval's fact-augmented key (value + extracted facts in one key) raised
  recall@k by 9.4 % and accuracy by 5.4 % on average; keeping the facts as separate index rows instead ("rank
  merging") was much worse — R@5 0.478 against 0.644.[^longmemeval] A key of the compressed fact alone, without the
  source text, did not help.
- **Lexical retrieval is competitive** on memory data: MemoryAgentBench puts BM25 first among retrievers overall (41.5
  against 38.2 for the best embedder, HippoRAG 2 at 41.6).[^memagentbench] LongMemEval finds dense ahead on its own
  data,[^longmemeval] and a fusion paper calls LongMemEval-S "a lexical regime where BM25 saturates" *(abstract)*.[^fusion]
- **Updates and forgetting are the weak spot.** All methods fail multi-hop selective forgetting (≤ 7 %) in
  MemoryAgentBench;[^memagentbench] about 10 % of LongMemEval errors retrieve the new fact but miss the old
  one;[^longmemeval] ForgetEval finds "Production failures are predominantly forgetting failures rather than recall
  failures, yet existing benchmarks measure only recall", with deterministic rules at "0% on cross-lingual"
  canonicalization and a mutation-time LLM hook recovering intent-aware deletion to 78–85 %.[^forgeteval] Five
  frameworks were found to return revoked records that "outrank" their replacement *(abstract)*.[^revoked]
- **Bad writes spread.** Agents follow similar memories, so wrong entries "replicate and amplify"; selective addition
  plus deletion gave about +10 % *(secondary)*.[^expfollow] Du's survey: "one bad write can pollute the store for many
  steps downstream", and "Nobody evaluates forgetting well".[^du]
- **Consolidation erodes detail** when it rewrites: ACE names "context collapse" and uses incremental structured
  updates instead.[^ace] Raw trajectories saved more steps than compressed memories in MemDev-Bench;[^memdev] verbatim
  chunks beat extracted artefacts on LoCoMo *(secondary)*.[^fidelity]
- **Sleep-time compute** — work on the store between sessions — cut test-time compute about 5× at equal accuracy
  *(abstract)*.[^sleeptime] memcue's `dream` is this shape.

### Safety

- Memory poisoning is real and cheap: SpAIware persisted instructions in ChatGPT memory for exfiltration
  *(secondary)*;[^spaiware] MINJA plants records through ordinary queries *(abstract)*;[^minja] Microsoft found "over 50
  unique prompts from 31 companies across 14 industries" seeding assistant memory and lists the markers "remember",
  "trusted source", "from now on".[^ms-poison]
- Defences found: provenance and trust scores with filtering at retrieval[^poison-defense]; Codex's `polluted` flag for
  threads with external content[^codex]; Copilot's citation check, which made a deliberately seeded false memory pool
  "self-heal"[^copilot-blog]; secret redaction before write (Codex, Anthropic)[^codex][^memtool]; delimiters alone are
  bypassable, datamarking much less so *(abstract)*.[^spotlight]

### Evaluation

- LoCoMo is a weak instrument: conversations fit in a context window (full context scored 72.9 J against 66.9 for
  Mem0)[^mem0]; about 6.4 % of the answer key is wrong and the judge accepted 62.8 % of deliberately wrong but on-topic
  answers[^penfield]; a filesystem agent with grep reached 74.0 %[^letta-bench]; the Zep–mem0 dispute over each other's
  numbers ended with a corrected 75.14 % for Zep and no agreement.[^zep-blog]
- LLM judges agree with people about 90 % on natural answers but are weakest on abstention and on on-topic-but-useless
  answers.[^longmemeval][^penfield]
- What production systems count: Copilot reports merge rate (83 → 90 %) and an A/B on review feedback[^copilot-blog];
  Codex makes the agent cite which memory it used and ranks by `usage_count`.[^codex] No public number for the share of
  injected memories an agent actually used was found.

## 3. Where memcue already stands

| Technique | Who uses it | memcue now |
|---|---|---|
| Add-only write, conflicts resolved later | mem0 v3, Letta dreaming | yes: extraction adds, `vet` and `dream` resolve |
| Gate that injects nothing, small cap | 2607.08716, VibeMemBench | yes: coverage gate, Jev ≥ 0.5, two facts |
| Lexical retrieval without embeddings | Letta MemFS, Codex grep, claude-mem FTS5 | yes; embeddings measured and dropped |
| Search phrases written with the fact | LongMemEval key expansion, LangMem queries | yes: `queries` column, same row (key merging) |
| Date and origin with each served fact | Anthropic guidance, Codex citations | yes: `learnt`, `scope` |
| Expiry for event facts | mem0 `expiration_date`, Supermemory | yes: `-expires` (#287) |
| Secret filter before write | Codex, Anthropic, Hindsight | yes: `internal/policy` |
| Exact duplicate before the model | mem0 MD5, Graphiti fast path | yes: `vet` treats equal text as a duplicate |
| Human or model review of candidates | Devin, Letta review pass | yes: candidates, `vet`, review page |
| Background consolidation | Letta dreaming, Codex phase 2, sleep-time compute | yes: `dream` |
| FTS query as OR of terms, not a phrase | (claude-mem got this wrong) | yes: `MATCH` with `OR` |
| Usage signal (served → used) | Codex citations, Copilot 28-day TTL | **no** |
| Citations to code checked before use | Copilot | **no** |
| Marking facts from web or MCP output | Codex `polluted` | **no**: tool results reach extraction as `RESULT`, source `tool` |
| Harm metric (memory-on vs memory-off) | VibeMemBench, MemDev-Bench | **no** |

## 4. Ignore

- **Knowledge graphs and community summaries** (Graphiti, Mem0g, MIRIX, HippoRAG, RAPTOR). Mem0g gained 1.5 J, lost on
  single-hop, doubled memory and tripled search latency;[^mem0] mem0 removed the graph from OSS;[^mem0-v3] HippoRAG 2
  leads BM25 by 0.1 overall in MemoryAgentBench.[^memagentbench] memcue's facts are short rules and decisions, not
  multi-hop narratives.
- **ADD/UPDATE/DELETE by an LLM at write time.** mem0 itself abandoned it for latency;[^mem0-v3] memcue's
  `vet`/`dream` already do the comparison off the hot path.
- **LLM query rewriting per prompt** (Query2doc, time-aware query expansion, HyDE). It puts a model call in the hook;
  time-aware expansion fails on small models;[^longmemeval] write-time `queries` covers the same gap once per fact.
- **Pseudo-relevance feedback (RM3).** Worse than BM25 on short noisy queries (recall@1000 0.51 against 0.77)
  *(secondary)*.[^rm3]
- **Lemmatization instead of Snowball for Russian.** No significant difference among stemmers on Okapi; no stemming
  halves MAP *(secondary)*.[^savoy]
- **Always-on dumps** (ChatGPT saved memories, Letta root files, Claude Code `MEMORY.md`). The card already plays this
  role within a budget; passive exposure lost to selective injection.[^proactive]
- **Pull-only memory** (agent calls a search tool). Agents rarely call it ("passive invocation") *(abstract)*;[^memcog]
  as an addition, see "try" 7.
- **Importance × recency × relevance scores, Ebbinghaus decay, heat scores** (Generative Agents, MemoryBank,
  MemoryOS). No validation found, and "Nobody evaluates forgetting well";[^du] a heat score that ranks recall feeds on
  itself.
- **Extraction on every tool call** (claude-mem). Costs that users report as a bug.[^claudemem-issues]
- **Vendor LoCoMo scores as a guide.** See §2, Evaluation.

## 5. Try

Ordered by expected value against cost. Each is a hypothesis for its own issue, measured the way the notes in this
directory measure.

1. **A harm metric.** Take the gold-set prompts that got a served fact and run the agent with and without it on a
   subset (`claude -p`, blind Opus comparison of the two answers), counting prompts where the fact made the answer
   worse. Source of the design: VibeMemBench's matched memory-off baseline and MemDev-Bench's Rescue/Harm.[^vibemem][^memdev]
   Cheap variant first: count served facts the labeller marks NOISE that the answer visibly followed.
2. **Check the labeller on on-topic noise.** Mix known-RELATED and known-NOISE facts into a labelling batch and count
   how many Opus calls USEFUL; label one batch twice for its spread. LoCoMo's judge passed 63 % of on-topic wrong
   answers;[^penfield] memcue's USEFUL/RELATED line is exactly that border.
3. **A usage signal and expiry by disuse.** Record per fact when it was served and, from the transcript at extraction
   time, whether the answer used it (cited a value, ran the command, followed the rule). `dream` then proposes
   retiring facts never served or never used in N days; card rules are exempt. Sources: Codex `usage_count` and
   30-day `max_unused_days`, Copilot's 28 days.[^codex][^copilot-docs] The extraction call already reads the
   transcript, so the "used" check costs no extra call.
4. **Mark facts from external content.** Tag tool results of `WebFetch`, `WebSearch` and `mcp__*` in the transcript
   given to extraction, give their facts a distinct source, and have `vet` refuse to auto-approve them and any
   candidate with imperative markers ("remember", "from now on", "always"). Sources: Codex `polluted`,
   Microsoft's markers, SpAIware.[^codex][^ms-poison][^spaiware]
5. **Citations to code, checked cheaply.** Extraction writes `path` or a symbol for facts about a repository; recall
   checks locally that the path exists (and, if cheap, that the symbol greps) before serving, and drops or marks the
   fact otherwise. Copilot reports this adds "no significant latency" and made seeded false memories
   self-heal.[^copilot-blog] Only for facts with a repository scope; preferences and hosts have nothing to check.
6. **Two extraction rules from mem0 v3's prompt.**[^mem0-v3] Resolve relative dates against the transcript's date, not
   today's; and record a change as a transition ("moved from X to Y because Z"), so the new fact carries the
   information that the old one is outdated and Jev can see it without an UPDATE. A prompt change, measurable on
   the extraction samples.
7. **Graphiti's guard phrases in the `dream` prompt.**[^graphiti] "Never mark a fact outdated unless the new fact
   explicitly makes it false; not from recency alone; never merge facts that differ in a number or a date." Measured
   as false retires on the labelled dream sample ([dream-triage-2026-10.md](dream-triage-2026-10.md)).
8. **Does the new fact beat the old one?** Take pairs where one fact was corrected or retired as outdated, restore
   the old one, and check which of the two the words and Jev serve for prompts about it. ForgetEval and the
   "revoked but still authoritative" study say this is where memories fail;[^forgeteval][^revoked] memcue has never
   measured it.
9. **Rewrite `queries` of old facts when a related new fact arrives** (A-MEM rewrites neighbours' tags, not
   content).[^amem] Only if 8 shows old facts winning or new ones missing.
10. **A pull command as a supplement.** The skill already exposes `memcue search`; check in transcripts how often the
    agent uses it when the hook served nothing. If almost never, as MemCog predicts,[^memcog] leave it.

## Contradictions and open questions

- **Push or pull.** Anthropic and Letta argue for just-in-time, agent-managed memory;[^anthropic-ce][^letta] MemCog and
  the proactive-agent paper show agents rarely pull and selective push wins.[^memcog][^proactive] The memory tool
  itself forces a read at the start of every task.[^memtool]
- **BM25 or dense.** LongMemEval finds dense ahead;[^longmemeval] MemoryAgentBench finds BM25 ahead;[^memagentbench]
  memcue's own measurement agrees with the second on its data.
- **Facts or raw text.** LongMemEval and MemDev-Bench find compressed facts lose information;[^longmemeval][^memdev]
  memcue stores facts because its content is rules and decisions, not conversation; whether that transfer holds is
  unmeasured. Keeping `evidence` (the verbatim words) next to each fact partly hedges it.
- **Context files.** Developer-written files helped on AGENTbench but not with Claude Code;[^agentsmd] a second study
  found no effect.[^twoagent]
- **mem0 v3 claims** retrieval surfaces current information first; its OSS scoring has no recency term.[^mem0-v3]
- **Open:** the share of served facts an agent uses (no public number); how much reranker jitter matters (no source
  found); whether code citations are worth it for a base where most facts are about hosts and preferences; how many
  facts to inject (no source gives a number).

## Quality metrics

- Sources cited: 52 — academic 27, official docs and source code 13, industry blogs 5, news 1, forums and issues 6.
  Academic and official together: 77 %.
- Read in full or through source: about 40; abstract or secondary only: marked inline.
- Numbers re-checked against a second reading: VibeMemBench, MemDev-Bench (abstract part), ForgetEval,
  2607.08716, the AGENTS.md study (abstract part changed the wording above).
- Not verified: Rescue/Harm figures of MemDev-Bench (body, one reader); A-MEM cost figures; mem0 v3 and Hindsight
  benchmark claims (vendor numbers, no reproduction); Cursor's reason for removing Memories (none given).

## Sources

[^vibemem]: Fan et al., *VibeMemBench: Evaluating Memory Systems for Coding Agents on Real Repository Coding Tasks*, arXiv 2609.23570, 2026. https://arxiv.org/abs/2609.23570
[^memdev]: *MemDev-Bench: Does Memory Help Code Agents?*, arXiv 2610.27524, 2026. https://arxiv.org/abs/2610.27524
[^agentsmd]: *Evaluating AGENTS.md: Are Repository-Level Context Files Helpful for Coding Agents?*, arXiv 2602.11988, 2026. https://arxiv.org/abs/2602.11988
[^twoagent]: *Do Context Files Help Coding Agents? A Two-Agent Ablation Study*, arXiv 2607.27250, 2026. https://arxiv.org/abs/2607.27250
[^gsmic]: Shi et al., *Large Language Models Can Be Easily Distracted by Irrelevant Context*, ICML 2023. https://arxiv.org/abs/2302.00093
[^longmemeval]: Wu et al., *LongMemEval*, ICLR 2025. https://arxiv.org/abs/2410.10813
[^proactive]: *Remember When It Matters: Proactive Memory Agent for Long-Horizon Agents*, arXiv 2607.08716, 2026. https://arxiv.org/abs/2607.08716
[^hn-chatgpt]: Hacker News, "I turned off ChatGPT's memory", 2026-02. https://news.ycombinator.com/item?id=47132001
[^hn-claude]: Hacker News, "Claude Memory", 2025-10. https://news.ycombinator.com/item?id=45684134
[^cc-23544]: anthropics/claude-code#23544, "Need ability to disable auto-memory". https://github.com/anthropics/claude-code/issues/23544
[^cc-85075]: anthropics/claude-code#85075, "Auto memory: months-old MEMORY.md silently loads…". https://github.com/anthropics/claude-code/issues/85075
[^cursor]: Cursor forum, "Are my memories gone?", 2025-11-25. https://forum.cursor.com/t/are-my-memories-gone/144057
[^copilot-blog]: T. Gazit, *Building an agentic memory system for GitHub Copilot*, GitHub blog, 2026-01. https://github.blog/ai-and-ml/github-copilot/building-an-agentic-memory-system-for-github-copilot/
[^copilot-docs]: GitHub Docs, *Copilot Memory*. https://docs.github.com/en/copilot/concepts/agents/copilot-memory
[^cc-memory]: Claude Code docs, *How Claude remembers your project*. https://code.claude.com/docs/en/memory
[^memtool]: Anthropic docs, *Memory tool*. https://platform.claude.com/docs/en/agents-and-tools/tool-use/memory-tool
[^codex]: openai/codex source: `codex-rs/memories/README.md`, `codex-rs/ext/memories/templates/memories/read_path.md`, `codex-rs/config/src/types.rs`. https://github.com/openai/codex/tree/main/codex-rs/memories
[^windsurf]: Windsurf/Cascade docs, *Memories & Rules*. https://docs.devin.ai/desktop/cascade/memories
[^claudemem]: claude-mem docs, *Architecture overview*. https://docs.claude-mem.ai/architecture/overview
[^claudemem-issues]: thedotmack/claude-mem issues #4138, #4965, #5023. https://github.com/thedotmack/claude-mem/issues/4138
[^mem0]: Chhikara et al., *Mem0: Building Production-Ready AI Agents with Scalable Long-Term Memory*, arXiv 2504.19413, 2025. https://arxiv.org/abs/2504.19413
[^mem0-v3]: mem0 docs, *Migrating OSS v2 to v3*, and `mem0/configs/prompts.py`, `mem0/utils/scoring.py`. https://docs.mem0.ai/migration/oss-v2-to-v3
[^zep]: Rasmussen et al., *Zep: A Temporal Knowledge Graph Architecture for Agent Memory*, arXiv 2501.13956, 2025. https://arxiv.org/abs/2501.13956
[^graphiti]: getzep/graphiti source: `prompts/dedupe_edges.py`, `utils/maintenance/edge_operations.py`, `dedup_helpers.py`. https://github.com/getzep/graphiti
[^letta]: Letta docs, *Memory* and *MemFS*. https://docs.letta.com/concepts/memfs
[^langmem]: langchain-ai/langmem source: `knowledge/extraction.py`, `reflection.py`. https://github.com/langchain-ai/langmem
[^amem]: Xu et al., *A-MEM: Agentic Memory for LLM Agents*, arXiv 2502.12110, and `memory_system.py`. https://arxiv.org/abs/2502.12110
[^hindsight]: *Hindsight*, arXiv 2512.12818, 2025. https://arxiv.org/abs/2512.12818
[^memoryos]: *MemoryOS*, arXiv 2506.06326, and BAI-LAB/MemoryOS source. https://arxiv.org/abs/2506.06326
[^memobase]: memodb-io/memobase README. https://github.com/memodb-io/memobase
[^supermemory]: Supermemory docs, *How it works*, *Graph memory*. https://supermemory.ai/docs/concepts/how-it-works
[^memagentbench]: Hu, Wang, McAuley, *Evaluating Memory in LLM Agents via Incremental Multi-Turn Interactions* (MemoryAgentBench), ICLR 2026. https://arxiv.org/abs/2507.05257
[^fusion]: Lysenstøen, *Training-Free Lexical-Dense Fusion for Conversational-Memory Retrieval*, arXiv 2606.04194, 2026. https://arxiv.org/abs/2606.04194
[^forgeteval]: *Control-Plane Placement Shapes Forgetting: An Architectural Study of Agent Memory Across Thirteen System Configurations* (ForgetEval), arXiv 2606.15903, 2026. https://arxiv.org/abs/2606.15903
[^revoked]: *Revoked but Still Authoritative*, arXiv 2609.08258, 2026. https://arxiv.org/abs/2609.08258
[^expfollow]: Xiong et al., *How Memory Management Impacts LLM Agents*, arXiv 2505.16067. https://arxiv.org/abs/2505.16067
[^du]: P. Du, *Memory for Autonomous LLM Agents: Mechanisms, Evaluation, and Emerging Frontiers*, arXiv 2603.07670, 2026. https://arxiv.org/abs/2603.07670
[^ace]: Zhang et al., *Agentic Context Engineering*, ICLR 2026. https://arxiv.org/abs/2510.04618
[^fidelity]: *Fidelity Before Structure: Verbatim Chunks Beat Lossy Artifact Extraction*, arXiv 2601.00821. https://arxiv.org/abs/2601.00821
[^sleeptime]: Lin et al., *Sleep-time Compute*, arXiv 2504.13171, 2025. https://arxiv.org/abs/2504.13171
[^spaiware]: The Hacker News, *ChatGPT macOS flaw could've enabled long-term spyware via memory function*, 2024-09. https://thehackernews.com/2024/09/chatgpt-macos-flaw-couldve-enabled-long.html
[^minja]: *MINJA: Memory Injection Attacks via Query-Only Interaction*, arXiv 2503.03704. https://arxiv.org/abs/2503.03704
[^ms-poison]: Microsoft Security blog, *AI Recommendation Poisoning*, 2026-02-10. https://www.microsoft.com/en-us/security/blog/2026/02/10/ai-recommendation-poisoning/
[^poison-defense]: *Memory Poisoning Attack and Defense on Memory Based LLM-Agents*, arXiv 2601.05504, 2026. https://arxiv.org/abs/2601.05504
[^spotlight]: Hines et al., *Defending Against Indirect Prompt Injection Attacks With Spotlighting*, arXiv 2403.14720. https://arxiv.org/abs/2403.14720
[^penfield]: Penfield Labs, *We audited LoCoMo*, dev.to, 2026. https://dev.to/penfieldlabs/we-audited-locomo-64-of-the-answer-key-is-wrong-and-the-judge-accepts-up-to-63-of-intentionally-33lg
[^letta-bench]: Letta, *Benchmarking AI Agent Memory: Is a Filesystem All You Need?*, 2025-08. https://www.letta.com/blog/benchmarking-ai-agent-memory
[^zep-blog]: Zep, *Lies, Damn Lies & Statistics: Is Mem0 Really SOTA in Agent Memory?*, 2025-05 (edited 2026-06), and getzep/zep-papers#5. https://www.getzep.com/blog/lies-damn-lies-statistics-is-mem0-really-sota-in-agent-memory/
[^rm3]: *LLM reformulation for tip-of-the-tongue retrieval*, arXiv 2602.10321, 2026. https://arxiv.org/abs/2602.10321
[^savoy]: L. Dolamic, J. Savoy, stemming for Russian ad hoc retrieval, JASIST 60(12), 2009. https://ideas.repec.org/a/bla/jamist/v60y2009i12p2540-2547.html
[^memcog]: *MemCog*, arXiv 2605.28046, 2026. https://arxiv.org/abs/2605.28046
[^anthropic-ce]: Anthropic, *Effective context engineering for AI agents*, 2025-09-29. https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents
