# The Hermes card keeps the lines that repeat the memory notes — October 2026

Status: **measured on a snapshot of a live base, 2026-10-27.** One change for
[#456](https://github.com/juev/memcue/issues/456). The snapshot is private and is not in the repository; this note
gives only aggregate counts of it, no card line, memory entry, host or path.

## What changed

The Hermes plugin dropped a card line when it shared enough words with an entry of `MEMORY.md` or `USER.md`
(`_without_memory_copies`, rule 3 of [card-memory-copies-424](card-memory-copies-424-2026-10.md)). A word rule cannot
tell a copy from another rule in the same words: "push to main without review" and "never push to main, only through
review" share almost every word, and so do "run the tests before every commit" and "run the tests before a commit only
when the change touches the store package". The filter dropped both kinds. By the owner's decision the filter is gone,
with no other filter in its place: the provider puts the card in exactly as `memcue session-card` renders it. A copy
costs a few lines. Nothing in the plugin merges a card line with a memory note now; whether the dream ever removes
such a pair was not checked, and this change does not rely on it.

Unchanged: the card budget (`session_card_chars` → `MEMCUE_CARD_BUDGET`), the binary's own rules (a mirror of a memory
note never reaches the card; a line whose opening repeats one already placed is left out — that prefix rule is #515 and
is not touched here), the built-in memory flags, the warning while its notes are injected, the mirror of its writes.

## Measurement

The snapshot: a SQLite backup of one installation's `facts.db` and a copy of its `MEMORY.md` and `USER.md`, taken
once on 2026-10-27 into a temporary directory. The one-off harness that measured it is not kept in the repository.
It loaded the plugin of this branch and the old plugin
(`git show 252ef54:plugins/hermes/memcue/__init__.py`) with the Hermes modules stubbed. It called
`system_prompt_block()` with the built-in memory on and off (`_builtin_memory_flags` patched). The binary was a
memcue 0.20.0 build, called directly, with every `MEMCUE_*` variable unset and the state directory inside the
temporary one. The host was the installation's own; the card budget was 1200 as in its config, and then 3500.
The harness passed no `cwd`. In that case the provider takes `TERMINAL_CWD` or the process directory as the session's
directory (`_session_cwd`), and neither was recorded. So it is not known whether the card held the decisions of a
repository or only the global rules and the rules the user stated. No model was called, and nothing was written
outside the temporary directory.

Limits: one base at one moment, one host, and an unrecorded working directory. Another base, another host or another
repository gives another card. The numbers below cannot be rerun without the private snapshot. A rerun must pass an
explicit `cwd` (a directory outside git for a card without repository decisions) and an explicit `host`.
Tokens are characters / 3.6, the ratio of the #424 measurement; no tokenizer was run.

| card budget | built-in memory | | card lines | card chars | ≈ tokens |
|---|---|---|---|---|---|
| 1200 (live config) | on | before | 6 | 910 | 253 |
| | | **after** | **7** | **1 184** | **329** |
| | off | before = after | 7 | 1 184 | 329 |
| 3500 (binary default) | on | before | 14 | 2 141 | 595 |
| | | **after** | **19** | **3 187** | **885** |
| | off | before = after | 19 | 3 187 | 885 |

With the memory on, the card is now the same card as with it off, byte for byte. On the live budget the change costs
**+274 characters (≈ +76 tokens)** at every session start and after every compaction, next to a memory block of about
3 300 characters that the prompt carries anyway; at the default budget it costs +1 046 characters (≈ +291 tokens).
The budget holds in both cases: the card stays under 1 200 and 3 500.

What came back: one line at the live budget, five at the default one. Each pairs with a memory entry at 6–15 shared
words (0.22–0.47 of both, 0.47–0.75 of the shorter one): all five are paraphrases of an entry, three of them with a
detail the entry lacks. None of them is an opposite or a conditional rule — this base has no such pair today; the
tests carry them instead.

## Tests

The plugin suite, run from the repository root after `go build -o bin/memcue ./cmd/memcue`
(`MEMCUE_BIN=bin/memcue python3 -I plugins/hermes/memcue/tests/test_provider.py`), passes, 68 tests, one skipped as
before. It uses temporary bases only. `test_the_card_keeps_what_the_memory_notes_say` puts five rules next to three memory notes —
two copies, the negated rule, the conditional rule and an unrelated one — and checks, for every combination of the two
memory flags, that each rule is in the block, that the block ends with the binary's card exactly, and that the block
does not depend on the flags. `test_a_card_of_copies_only_is_a_card` replaces the test that expected no card.
Run against the old plugin, both fail and so does the card check of `test_a_memory_hermes_turned_off_is_no_note`; the
old filter dropped four of the five rules, the negated and the conditional one included.

Not measured: whether the dream removes any of these duplicates, and what the extra lines change in the answers.
