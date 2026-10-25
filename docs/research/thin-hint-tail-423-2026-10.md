# The thin-answer hint counted the limit, not the tail — fixed, October 2026

Status: **measured on a live base, 2026-10-26.** One change for [#423](https://github.com/juev/memcue/issues/423).

The hint after a thin answer runs the same search once more with `-min-score 0` and reports how many facts that added.
The second call kept the caller's `-n`, so the number it printed was `limit − served`, not the size of the tail: on
the same one-fact answer it said 4 at the default, 9 at limit 10 and 19 at limit 20, and the number could not move
with the base.

Now the wide arm asks for its own number, `min(4 × limit, 20)`, and the answer itself stays capped by the caller's
limit. Measured through the plugin on a copy of the live base, one fact above the floor each time:

| query | limit | before | after |
|---|---|---|---|
| `stale_write_blocked ошибка` | default (5) | 4 more | **19 more** |
| `stale_write_blocked ошибка` | 10 | 9 more | **19 more** |
| `stale_write_blocked ошибка` | 20 | 19 more | 19 more |
| `телеграм dm топик баг` | default (5) | 4 more | **14 more** |
| `телеграм dm топик баг` | 10 | 9 more | **14 more** |

The number is now a property of the base and the query, and the message still names what it shows: the facts that hold
the words and sit below the floor, with `min_score 0` as the way to read them and the limit still capping the answer.
The test drives a stub binary that records each argv: the first call keeps the caller's `-n`, the second carries
`min(4 * limit, 20)` together with `-min-score 0`, and the hint counts what the wide call returned beyond the served
facts.

What is not measured: the price — the wide arm now returns up to 20 facts instead of 5, so a thin answer pays a larger
second process; the hint fires only on an answer of one fact or none, and the second call's own latency is tens of
milliseconds on a base of this size.
