# Codex CLI pull-mode integration

Implemented contract for [#670](https://github.com/juev/memcue/issues/670). The owner will check authenticated Codex
CRUD, lifecycle and sandbox behavior locally after merge; those checks remain unverified. Independent review,
regression tests and CI remain required before merge. Keep #670 open pending the owner's result.
Setup and limitations live in [the integration guide](../../plugins/codex/README.md).

- R1: A local plugin or standalone handler supplies policy on Codex SessionStart startup/resume/clear/compact/fork.
  It accepts Codex 0.167.0's input, emits `SessionStart` additionalContext, and adapts maintenance-only `SessionCard`.
  It never calls session-start, searches facts automatically, reads transcripts, or supplies a rules card.
- R2: Every hook and later shell invocation reloads one explicit absolute binary/base/state/host configuration.
  No missing configuration selects a default Claude base. The hook uses its input cwd; later calls use process cwd.
  Existing native scope, shelf, expiry, provenance, JSON and history semantics remain intact.
- R3: The working model searches rules/decisions at task start and system switches, searches before writing, and
  chooses add/correct/retire. English-first instructions cover initial queries and new/corrected content, preserve
  exact identifiers/quotations and user-facing language, and allow legacy-language fallback. They do not enforce a language in storage.
- R4: The launcher clears inherited MEMCUE_* settings except explicit backend transport settings and uses the
  configured base/state/host. It does not launch a background model. The working model gives a reason in each
  retirement call; native `history` reads the reason. Existing content stays as stored; language alone is no reason
  to correct a fact.
- R5: Invalid hook input/config/response and failed commands produce bounded visible diagnostics without a blocking
  decision. Normal CLI errors exit nonzero. Input size, child time and output reads are bounded; a failed write may
  still have persisted, so callers check before retrying. Native maintenance checks can silently suppress their own errors.
- R6: Hook trust remains Codex's review workflow. Install/update/remove preserve other instructions/configuration
  and base/history. The instructions-only fallback is explicitly rendered and manually added as one marked block.
  The integration does not alter sandbox policy; outside-workspace access requires scoped permission.

Tests: `plugins/codex/tests/test_adapter.py` verifies R1–R6 at adapter boundaries, including delivered text;
`test_cli.py` verifies fresh-process persistence, scopes, history, shelves, expiry, retirement reasons;
`plugins/test_cross_harness.py` checks delivered Codex instructions alongside the existing integration contracts.
Actual Codex hook trust/lifecycle and model-driven behavior require the separate synthetic runtime procedure in the guide.
