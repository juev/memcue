# 0.20.0 after the upgrade: the post-release check, October 2026

Status: **post-release check, 2026-10-26**, before any real session on 0.20.0. Every write went to a copy of a base in
a scratch directory; the live bases took no test fact.

Verdict in one line: **0.20.0 is installed on Mac, loffe and Hermes, the new kinds are written, found and kept off the
card as the README says, search answers are byte-identical to 0.19.0 on 76 real queries; the DSH desktop profile
still runs plugin 0.4.0 and its config sets an endpoint, so `memcue_add` there asks DeepSeek for search queries.**

## Installation

| host | binary | plugin |
|---|---|---|
| Mac, Claude Code | 0.20.0 | 0.20.0 (`memcue.sh version` 0.20.0) |
| loffe, Claude Code | 0.20.0 | 0.20.0 |
| loffe, Hermes | `memcue-real` 0.20.0 | `plugin.yaml` 0.20.0, `/opt/loffe` at `f07fbca`; no root-owned file under the data dir |
| Mac, DSH `desktop` profile | 0.20.0 through `~/.local/bin/memcue-dsh` | **`memcue-dsh` 0.4.0** (npm has 0.20.0) |

`memcue reindex -dry-run` on all three live bases: 0 live facts with a number but no number term.

## Tests

- `make test test-plugins`: pass (Go and 65 plugin tests).
- Hermes provider tests in the container: 65 of 68; the three failures are the card tests of the open #504.

## The new kinds (#505), on a copy of the Mac base

| case | result |
|---|---|
| `add -kind procedure` | stored, no expiry, scope `repo:github.com/juev/memcue` |
| `add -kind event` | expires 2026-11-02 (today + 7) |
| `add -kind event -expires 2026-12-01` | keeps 2026-12-01 |
| `MEMCUE_EVENT_TTL_DAYS=0` / `30` / `abc` | no expiry / 2026-11-25 / falls back to 7 days |
| `add -kind event -expires 2026-10-20` (past) | accepted; left out of search, found with `-expired` |
| `add -kind repo`, `other`, `banana` | refused, exit 1, the message lists the six kinds |
| `search -kind procedure`, `-kind event` | find the test facts |
| session card (`session-start -part 1..3`, `session-card -dream-tools`) | no test fact on it |

The same `add` and `search` through the Hermes binary in the container, on a copy of its base: same result.

## Search: no change

76 queries of the Mac `search.log`, each with its repository as `-cwd`, through the 0.19.0 and 0.20.0 binaries on
one copy of the base: 76 identical outputs, 0 failures, 2 empty answers in both. End to end p50 13.1 against 12.8 ms,
p90 15.7 against 15.8 ms. Nothing in 0.20.0 touches the search code, and the run agrees.

## Found on the way

- **DSH desktop profile.** `memcue-dsh` there is 0.4.0 (installed 2026-10-21); the profile is owned by the app, so it
  updates only from its Plugins page. Its config sets `apiUrl` and `apiKey`, the plugin turns them into
  `MEMCUE_API_URL` and `MEMCUE_API_KEY` (`plugins/deepseek-harness/src/index.mjs`), and with `MEMCUE_API_URL` set
  `add` asks the endpoint for the fact's search queries (`cmd/memcue/facts.go`, `expandStored`). The removal of these
  variables on 2026-10-26 did not reach this path. The last DSH session was on 2026-10-23.
- **Old events have no expiry.** 276 of the 277 live event facts of the Mac base have none: they came from the
  migration of 2026-10-19 and from 2026-10-20 and 2026-10-25, before `add` applied the TTL. Many are incident and
  measurement history that stays useful; the TTL of #505 covers only new writes, so this is how it is meant to work,
  not a regression.
- **`extract.log` and `vet.log`** were last written at 13:58Z, before 0.19.0 was published at 14:12Z: nothing has
  extracted since.

Not checked: a real Claude Code, Hermes or DSH session on 0.20.0, so it is unknown whether the model picks the new
kinds when the user asks to remember steps or an event.
