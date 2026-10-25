# The default limit against the model's habit: five stays — October 2026

Status: **measured on a live base, 2026-10-26.** The decision [#425](https://github.com/juev/memcue/issues/425) asked
for. Names of private hosts and repositories are placeholders.

## What the model does

In 57 recorded `memcue_search` calls, **29 passed `limit`**: 10 thirteen times, 5 ten times, 6 three times, 20 twice,
8 once; no call passed `in`, `kind`, `since` or `min_score`. The schema says five and half the calls overrode it, so
the question was whether the model's choice is the better point.

## What the tail holds

On the same copy of the live base, the same twenty user prompts, the answer at the default and at 10, graded `U` (it
answers), `R` (on the topic), `N` (unrelated), one rater:

| | default (5) | limit 10 |
|---|---|---|
| facts delivered | 73 | 113 |
| characters | 13 257 | 21 419 (+62 %) |
| facts the higher limit adds | — | 40 |
| **of those, useful** | — | **1** |
| of those, on the topic | — | 23 |
| of those, unrelated | — | 16 |

The floor, not the limit, ends the answer: the higher limit adds 40 facts of which one answers the question and 16 are
unrelated, for 62 % more context on every call. The one useful fact was not lost — the same prompt found its useful
fact at the default as well.

## The decision

**Five stays the default.** The model's habit is not evidence that ten is better, and measured it is worse: 40 % more
characters per call, one useful fact, sixteen unrelated ones. Instead the schema now says what the limit is for —
`the score floor usually ends the answer first, and raising the limit adds the tail: raise it only when the answer you
got is thinner than the question needs` — and `min_score` keeps naming the wide net for the case where the floor is
what hid the facts. `in`, `kind`, `since` and `min_score` stay in the schema: each names the case it is for, and the
hint after a thin answer names `min_score 0` explicitly, which is the route the model needs without knowing the base.

A later change can revisit the number on a base where the floor is not the end of the answer; the harness for that is
the same two arms run above.

## Tests

The plugin suite pins `_SEARCH_FACTS == 5` (the measurement point of #372) and that the `limit` description names both
the default and the tail; `go vet ./...` and `go test ./...` are clean.
