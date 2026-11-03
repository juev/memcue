# The global part of the card from selected facts, on real prompts, November 2026 (#623)

Status: **measured 2026-11-03 on 40 real prompts, two arms, two repetitions each.** **A global part limited to behaviour
is not worse than the current one; the decision rule of #623 passes.**

The question of #623: the card exists so that a rule the agent would not think to search for still reaches it. Should
its global part carry only such rules, the facts with no moment for search, and leave the repository part as it is?
[card-trim-606](card-trim-606-2026-11.md) trimmed the whole card, the repository part included, and scored −0.09
[−0.20, +0.01]; most of what it removed were global facts about one repository. This run keeps the repository part
whole, on a base where the facts #607 and #617 moved already sit in their repositories, and limits only the global
part.

In short:

- **select scores 1.23 of 2, current 1.26.** By prompt, select wins 8, ties 21 and loses 11; the mean difference is
  −0.03, 95% interval [−0.14, +0.08]. The decision rule of #623 (interval includes zero, mean not below −0.05) passes.
- **The difference is smaller than the noise of one repetition.** Graded together, the first repetition alone gives
  −0.11 and the second +0.05; the two repetitions of one arm differ on 14 (current) and 15 (select) of 40 prompts.
- **The card shrinks by a fifth.** 43 facts instead of 60 and about 13,300 characters instead of 16,300 a session; the
  whole run costs $34.91 against $36.51 (4%).
- **The global part still holds facts about one repository.** select removed 21 facts a prompt, about 13 of them
  global facts that the labeller put in the repository class. The move of #607 and #617 took 34 such facts off
  global; 21 distinct ones remain on the cards of this sample.
- **The losses are not one class.** The removed facts look alike in the prompts select lost and in those it won.
- Recommendation: an implementation issue for how a fact joins the global part of the card. The global facts about one
  repository should move to their repository in any case, as #607 did for the first 34.

## Arms

Both arms are one `claude -p --model opus` in plan mode on a clone of the prompt's repository as of the prompt, with the
memcue plugin, the user's own settings and hooks, and a throwaway copy of the fact base as of the prompt, as in #606.
The binary is `main` at f3ab9de (after #607, #613 and #617) with the research-only
[card-keep.patch](scripts/card606e2e/card-keep.patch). The arms differ only in the card:

- **current**: the card of `main`.
- **select**: the same card without the global facts that are not labelled `behaviour`. Every fact of another scope
  stays. [keep623.py](scripts/card606e2e/keep623.py) writes the keep list.

The snapshots were built again from the live base after the move of #607 and #617. A relabel keeps the creation time of
a fact, so a snapshot as of an older prompt shows the fact in its new repository. That is what both arms are meant to
see: the card as it is now, not as it was before the move.

## Prompts and labels

The 40 prompts and their references are those of #606 (see [card-trim-606](card-trim-606-2026-11.md#prompts)). The
labels are the #606 labels; 9 global facts on the new cards had none, and the same Opus labeller gave them one ($0.12):
7 behaviour, 2 knowledge.

## The cards

| arm | facts | characters | global facts | of them behaviour |
|---|---|---|---|---|
| current | 60 (42–87) | 16,307 | 46 | 24 |
| select | 43 (30–67) | 13,324 | 24 | 24 |

The current card does not hold every behaviour fact: the card budget cuts some of them off. A smaller global part
leaves room, and select carries 4.5 facts a prompt that did not fit on the current card: 3.6 global behaviour facts
and 0.9 repository facts. A real implementation would do the same.

Removed by select, a prompt on average, by class:

| removed | lost prompts (11) | won prompts (8) | all prompts |
|---|---|---|---|
| repository | 12.1 | 12.8 | 12.8 |
| knowledge | 2.3 | 2.6 | 2.5 |
| command | 2.0 | 2.2 | 2.0 |
| file | 1.9 | 2.1 | 1.9 |
| outdated | 1.8 | 1.9 | 1.7 |
| behaviour | 0.1 | 0.2 | 0.2 |

The few behaviour facts listed as removed (8 in all, every one global and on the keep list) fell off under the card
budget, not by the keep list.

## Results

160 runs (40 prompts × 2 arms × 2 repetitions), none failed, none without a card, $71.42. Graded by
[grade606.py](scripts/card606e2e/grade606.py) with `E2E_ARMS=current,select`: one Opus call a prompt reads the prompt,
the reference and the four answers under letters in a per-pass order; two passes, $12.46, and $9.04 for grading the
first repetition alone before the second ran. A flag counts when both
passes raise it; a score is the mean of the two.

| arm | score | 2s | 0s | wrong | asks | past | turns | tools | cost | median time | searched | searches a run | card |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| current | **1.26** | 28 | 12 | 8 | 38 | 29 | 11.3 | 10.3 | $36.51 | 54 s | 67 of 80 | 2.9 | 60 facts |
| select | 1.23 | 24 | 12 | 8 | 39 | 29 | 11.0 | 10.1 | $34.91 | 54 s | 69 of 80 | 2.5 | 43 facts |

By prompt, each side the mean of its two repetitions, 95% bootstrap interval of the mean difference (5,000 resamples):

| pair | wins / ties / losses | mean diff | 95% interval |
|---|---|---|---|
| select vs current | 8 / 21 / 11 | −0.03 | [−0.14, +0.08] |
| repetition 1 only | 2 / 31 / 7 | −0.11 | |
| repetition 2 only | 9 / 23 / 8 | +0.05 | |

Noise: the grader gave the same score in both passes to 133 of 160 answers, with no 0↔2 flip. The first repetition was
graded once on its own (−0.06 by prompt); the table uses the joint grading of all four answers.

Two losses of the first repetition were read by hand:

- In two prompts about releasing a project, select did not have the global facts about the release order of one
  repository. Those facts are global by mistake: they name one repository and belong to it.
- One prompt lost 2 points in the first repetition on an answer that missed the cause; none of the removed facts was
  about the subject. One prompt that select won had a current answer that read the installed binary outside the
  clone and answered about the wrong version.

## Isolation

The isolation of [card-trim-606](card-trim-606-2026-11.md#isolation) applies. Runs whose Bash commands named the live
repositories, base or data: 19 current, 17 select; the shims refused nearly all of it. The installed binary under the
home directory is not among the closed paths, and one current answer read its version.

## Decision

The rule of #623 passes: −0.03 with an interval across zero. An implementation issue follows for how a fact joins the
global part: a flag at `memcue add`, set by default for the user's own rules, with dream proposing candidates the user
confirms. Before it, or with it, the remaining global facts about one repository move to their repository: they make up
most of what select removed, and a fact about one repository has a moment for search in that repository.

## Limits

- One run of 40 prompts, two repetitions. The interval is twice as wide as the difference; the conclusion is "select
  does not hurt", not "select helps".
- What select saves is card space (a fifth of the card, about 3,000 characters a session), not cost or time: the run
  cost 4% less, and the time did not move.
- The prompts are the #606 sample: eight days of one person's work, chosen among prompts judged to need memory.
- One labeller (Opus) for the classes; the keep list follows its labels.
- Plan mode, without network and without writes; the reference comes from a session that had every tool.
- Claude Code only. Hermes and DSH build a smaller card (#487) and were not run.
