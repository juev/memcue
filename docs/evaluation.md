# Evaluation

What was measured while memcue was built (October 2026), on one developer's Claude Code memory and sessions. The
samples are small and come from one person's work, so read the numbers as direction, not as benchmarks. The data
itself (real prompts, notes and transcripts) stays private; the method is described so it can be repeated on other
data.

Labelling was done blind by separate Claude agents, not by people: each got the transcript or the prompt and the
candidate facts without knowing which system produced them, and labelled every fact against the source.

## Recall: what reaches the agent

**Setup.** The starting point was Claude Code's built-in memory of one user: 312 notes in files, indexed by
`MEMORY.md`, split by repository. The question was whether a hook that picks notes or facts by the words of the
prompt gives the agent what it needs without filling its context with noise.

**Labels.** For a set of real prompts, every item a system injected was labelled USEFUL (helps with this prompt),
RELATED (on topic, not needed) or NOISE; every prompt was also labelled for whether it needed memory at all.

### Holdout: 100 real prompts

Each system was tuned on a separate development set and run once on prompts it had not seen.

| System | Items injected | Useful | Noise | Notes |
|---|---|---|---|---|
| memcue (facts, final) | 55 | 18 (33 %) | 21 (38 %) | useful facts reached 12 of the 41 prompts that needed memory; 16 facts went to prompts that did not |
| memcue v1 (paragraphs of notes) | 53 | 9 (17 %) | 31 (58 %) | |
| holographic 0.17.4 prefetch | 219 | 8 (4 %) | 190 (87 %) | injected something into 61 of 100 prompts |

The last two rows were measured on another 100 prompts of the same kind (a holdout is used once). What changed
between v1 and the final version: the base holds short self-contained facts instead of paragraphs of notes, facts
from every repository are searched, and at most two facts are given.

Lexical search is the limit: a prompt that shares no rare words with the fact it needs gets nothing. 12 of 41 is
the honest coverage of what the prompts needed.

### End to end: does the agent answer better

30 questions whose answers sit in the memory (10 about notes of another repository than the one the session runs
in), each asked once with and once without memcue through `claude -p`, the order alternated. Answers were checked
against expected values.

| | Without memcue | With memcue |
|---|---|---|
| Correct answers | 27 / 30 | 30 / 30 |
| Questions about another repository | 7 / 10 | 10 / 10 |
| Turns per answer, mean | 3.2 | 2.2 |
| Cost per answer, mean | $0.65 | $0.61 |

The hook injected the fact the answer came from in 26 of 30 runs. One run per question: three questions flipped
from wrong to right and none the other way, which is a direction, not a significant result.

The hook takes about 10 ms per prompt with a base of 2335 facts.

## Writing: how facts get into the base

### Migration of existing notes

312 notes of the built-in memory → 2335 facts on Claude Opus, $39.83. On a sample of 30 notes (205 facts) every fact
was checked against its note: with the first prompt 16 % of facts carried the model's own conclusions; after the
prompt forbade anything not written in the note, 2.5 %; wrong facts: none in either.

### Extraction from sessions

10 recorded Claude Code sessions from nine repositories, 22–272 thousand characters of transcript each. Every fact
was labelled SUPPORTED (said by the user or confirmed by tool output), INFERRED (a guess, or an agent's proposal
presented as the user's decision) or WRONG (contradicts the transcript, or keeps a state the session later changed),
and DURABLE or EPHEMERAL. The labeller also listed the durable facts each session established; coverage is how many
of those an extractor kept.

First run on Opus, before the prompt was tightened: 101 facts, 3 WRONG and 5 INFERRED. All three wrong facts came
from the longest session, read in two parts: the first part did not know a later part changed the state. The fixes:
a decision is the user's only if the user stated or confirmed it; a fixed problem is recorded with its fix; later
parts of a long session see the facts of earlier parts.

Comparison of models after the fixes, blind (A/B with the model hidden):

| Model | Facts | Wrong / inferred | Coverage of durable facts | Cost of the run |
|---|---|---|---|---|
| Claude Opus | 102 | 0 / 1 | 84 of 96 (88 %) | $4.97 |
| Claude Sonnet | 69 | 0 / 3 | 70 of 96 (73 %), nothing at all in 2 sessions | $2.38 |

Then DeepSeek (`deepseek-flash`, through the OpenAI-compatible endpoint) against Opus on nine of the sessions (the
tenth had grown since the Opus run):

| Model | Coverage of durable facts | Wrong / inferred | Ephemeral facts | Cost of 10 sessions |
|---|---|---|---|---|
| Claude Opus | 81 of 93 (87 %) | 0 / 4 | 5 | $4.97 |
| DeepSeek flash | 69 of 93 (74 %) | 1 / 3 | 24 | $0.17 peak, $0.08 off-peak |

On the first DeepSeek run one long session lost almost everything: asked to return the whole list after its second
part, the model returned only the new facts. Since then a later part returns only new facts and the numbers of
earlier facts it outdates; earlier facts stay unless named. The DeepSeek row is after that change; the one wrong
fact is a misread number (a byte length taken for a vector size).

About 30 of the 101 facts of the first run repeated what the base already held, because the base had been built
from notes the same sessions wrote. With the built-in memory off, extraction is the only writer and that source of
duplicates goes away.

### Extraction by patterns

holographic can store user sentences that match patterns such as "I prefer …" or "we decided …". Its patterns found
nothing in the 55 user messages of the 10 sessions and nothing in 280 real prompts, while the labellers found 96
durable facts in those sessions. memcue does not extract by patterns.

## Cost in daily use

Extraction is the only part that calls a model. On the author's volume (about 170 sessions a week) Opus extraction
comes to roughly $120 a week at list prices; on a subscription that is usage of the plan, not money. DeepSeek is about
thirty times cheaper and keeps three quarters of what Opus keeps.

## Repeating the measurements

`memcue eval -set FILE` runs a labelled set against the base and prints how many questions found their fact and,
from the labels, how many injected facts were useful, related or noise; `-unlabeled FILE` writes the recalled
(prompt, fact) pairs that still need a label, and `-min-matched`, `-min-coverage`, `-max-facts` try other
thresholds. The set format is
the `evalSet` type in `cmd/memcue/eval.go`. `memcue extract -dry-run TRANSCRIPT.jsonl` prints what a session would
add, with tokens or cost, without storing it.
