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
