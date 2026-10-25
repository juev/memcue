# An empty answer that has facts below the floor hands over the first of them, October 2026

Status: **measured on a live base, 2026-10-26.** One change for
[#439](https://github.com/juev/memcue/issues/439). Placeholders stand for private hosts and services.

## What was wrong

A count is not an answer. When the answer held nothing, the hint after it said how many facts sat below the score
floor — a number the model cannot read, cannot quote and cannot judge, while the second search that produced it had
already been paid for (15–21 ms against 8–10 ms for an ordinary answer).

An empty answer with a non-empty tail is not rare on a message: since #417 a fact has to hold two of a message's terms,
so a message whose words each appear in some other fact leaves the floor with nothing, while the wide arm — which skips
that rule — still finds them. Measured on a copy of the live base with one such message
(«…с headscale и как вообще дела у нас с этим сервисом…»): **0 facts above the floor, 11 below**.

## What changed

- An answer of **zero facts** whose wide arm returns some now carries the first **two** of them, each with
  `below_floor: true`, and the hint says what they are: `11 facts hold the words but sit below the score floor; the first
  2 come with below_floor: true, and min_score 0 reads the rest.`
- An answer of **one** fact keeps today's shape: the fact is the answer, and the hint counts the tail.
- An answer with two facts or more pays nothing: no second call, no marker, the same characters as before.
- A base whose tail is empty produces today's answer, with no marker and no wasted call.

The same base, the same message, through the plugin before and after:

| | before | after |
|---|---|---|
| facts | 0 | **2, marked `below_floor`** |
| hint | `11 more facts hold the words…` | `11 facts … the first 2 come with below_floor: true …` |

The facts handed over are also recorded by the usage log, as a normal answer's are, so a fact first seen this way is
still counted when the reply uses it.

## Tests

`test_an_empty_answer_hands_over_the_first_facts_below_the_floor` drives a stub binary that answers `[]` on the floor
search and two facts on the wide one, and checks the facts, the marker and the wording; the count-only case and the
answer of two facts or more keep their tests. `go test ./...` green, plugin suite 78 tests OK.

## What is not measured

How often the model uses a marked fact rather than widening the search — that needs a tool-loop harness, and the point of
the change is only that there is something to read.
