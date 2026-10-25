# The session card and the memory block: what the card can and cannot drop — October 2026

Status: **measured on a live base and a live card, 2026-10-26.** One change for
[#424](https://github.com/juev/memcue/issues/424). Names of private hosts and repositories are placeholders.

The prompt carries the memory tool's block (`MEMORY.md` + `USER.md`) at every turn and cannot drop it; the session
card arrives next to it and repeated it: on a live card 5 of 16 lines stated something the memory block already says —
the skills rule twice, the anonymising rule, the reporting rule and the installation-state note.

## What the card drops now

Three rules, in the order they run:

1. **A mirror of a built-in memory note is not on the card at all.** The note is the prompt's own; the mirror is a
   second copy of it, and the memory tool owns its pruning (`CardFacts`, source `memory`). Measured on this base: no
   mirror reached the card, so this rule alone changed nothing — the duplicates came from facts the extractor wrote,
   not from mirrors.
2. **A line whose opening repeats a line already on the card is left out** (`renderCard`, first 60 characters of the
   normalised text). One rule split into two facts becomes one line. Measured on this base: no pair shared its
   opening, so this rule changed nothing either.
3. **A line the memory notes already state is left out** (`_without_memory_copies` in the Hermes plugin, which is
   where both texts meet). A line and a memory entry are compared as sets of words cut to five letters; a line is
   dropped when it shares a third of both or two fifths of the shorter one, and never on fewer than five words.
   Measured on the live card, the lines that copy an entry score 0.30–0.43 of both and 0.40–0.75 of the shorter one;
   the lines that do not stay under 0.27 and 0.29.

Result on the live card of this installation:

| | before | after |
|---|---|---|
| lines | 16 | **11** |
| characters | 3 181 | **1 995** (−37 %) |
| ≈ tokens at every session start and after every compaction | 890 | **558** |

The five lines dropped are exactly the ones the issue named, and the eleven kept are the user's rules the memory block
does not carry — the report order, the squash-merge instruction, the skills-repository rules, the measurement-first
rule. `card.log` keeps the rendered card's `chars` and gains `excluded`: how many facts the card left out as a repeat,
so the cost stays visible next to what it saved.

## What the card still repeats

A paraphrase in other words is beyond a text rule. A fact that restates a memory entry with different vocabulary —
same instruction, other nouns — stays on the card, and the honest fix for it is not a threshold but the extraction
side: such a fact should not be written at all, because the memory entry is what the user stated. The retire pass
after this release removes the ones this base already holds.

## Tests

`go vet ./...` clean, `go test ./...` all packages ok (the card's dedupe is covered by
`TestSessionCardDropsARepeatedRule`, the mirror rule by `TestCardFactsSkipMemoryMirrors`, the log by
`TestSessionCardLogsTheExcludedFacts`); the plugin suite passes with `test_the_card_leaves_out_what_the_memory_notes_say`
and `test_a_card_of_copies_only_is_no_card`.

What is not measured: a base whose card is mostly repetitions, and a second labeller for those five lines — the sets
they were dropped by are above, so the decision can be re-judged from the text.
