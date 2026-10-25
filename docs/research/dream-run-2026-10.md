# The dream through the extraction endpoint, October 2026

Status: **measured 2026-10-25 for [#401](https://github.com/juev/memcue/issues/401), shipped with reasoning on.**
A Hermes gateway has no session model to hand a dream packet to, and the cron job meant to run the dream was never
set up, so its dream ran only when someone asked the agent. `memcue dream -run` sends a due packet to the endpoint
the extraction already uses and applies the answer. The Hermes plugin starts it after each extraction.

The question this note answers: should that call reason? Every other endpoint call of memcue disables reasoning on
DeepSeek, because it spent 97–99 % of the output of a split call (#232).

## Setup

- Two packets, each from a copy of a live base with its dream state (`dream.at`, `dream.seen`, `dream.merge`):
  a Hermes base of 388 facts and a Claude Code base of 2,858 facts. `MEMCUE_DREAM_EVERY=1m` makes the dream due at
  once.
- deepseek-flash at `api.deepseek.com`. Without reasoning: `thinking: disabled`, temperature 0, as the other endpoint
  calls. With reasoning: neither field.
- One run per cell. Every decision of each run was read against the facts it acted on.

## Results

| | Hermes packet | Claude Code packet |
|---|---|---|
| without reasoning | 16 s, 13,126 tokens in, 4,118 out | 33 s; the answer was cut at the token limit and nothing was applied |
| with reasoning | 94 s, 13,151 in, 22,152 out | 137 s, 23,602 in, 29,132 out |

What the answers did:

- **Without reasoning, Hermes packet.** 40 translations, all faithful. One retire was wrong: a fact listing the fields
  of `search.log` was retired as a duplicate of a fact about the search tool, which does not mention them. Two facts
  that each named two domains were split into four, although the task's rules call a list of one property's values
  not compound.
- **With reasoning, Hermes packet.** 40 translations. Three retires, each a real duplicate. Two corrects rewrote a fact
  with its own text, which changes nothing. One retire dropped a detail the remaining fact lacks: where a second
  skill lives. Two splits, both into independent statements.
- **With reasoning, Claude Code packet.** 40 translations. Six splits into atomic statements that each name their
  subject. One retire: a fact saying the user had left memcue, by a later fact saying the user switched back. That
  pair was in different groups of the same packet, and a session model answering the packet by hand could not
  retire it under the group rules.

## Decision

`dream -run` lets the model reason. A dream retires and rewrites facts with nobody to review them, and the
no-reasoning answers both lost a fact and failed outright on the larger packet. The price is 5–7 times the output
tokens and up to 2.5 minutes, in a background process started at most once an hour while the gateway is in use.

Not measured: more than one run per cell, the token limit with reasoning on a packet larger than these, and the cost
in money.

## The whole Claude Code base with `-run -all` (#405)

One run of `memcue dream -run -all` on a live Claude Code base of 2,861 facts, through `claude -p` on Opus (no
endpoint), 2026-10-25.

- **Time and money.** 106 passes in 3 h 11 min, 108 s a pass on average; $63.16 in all, $0.22–1.97 a pass, $0.60 for
  most. At the hourly default the same work takes 72 or more dreams, three days and more.
- **Work done.** 4,024 translations; 612 compound facts split into 1,483 parts; 1,414 facts given search queries;
  164 retired and 88 corrected as duplicates or outdated. The base grew to 3,584 live facts, each indexed.
- **Why it took longer than the translation debt suggests.** The debt was 2,771 facts, about 70 passes at 40 a pass,
  and a pass did translate 40 facts in 100 of 106. But every split part is a new fact that needs its own translation
  and its first dream: in the first 67 passes the dream split 488 facts, and for an hour the debt fell by about 15 a
  pass. Once the compound facts were gone it fell by 40.
- **End.** The last pass found no work and the run stopped on its own ("done after 106 passes"), not on the stall or
  pass limit. 3,577 of 3,584 facts carry a translation; the other 7 are provisional facts waiting for review, which
  the dream does not take.

Not measured: the quality of these answers fact by fact; the earlier sections read three packets by hand, this run was
read only through its counts.
