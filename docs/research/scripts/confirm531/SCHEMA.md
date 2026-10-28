# Fixtures of the #531 confirming run

Everything an agent run sees comes from `fixtures/`. The stand copies it into a fresh container per run; nothing in it
may be real: invented systems, hosts, people and numbers only, and every web address under a reserved name
(`*.example`, `example.com`, `example.org`). `check_fixtures.py` validates the layout and prints counts only.

## Layout

```
fixtures/
  scenarios.json        the runs: what the agent is asked, in which workspace, over which seeded base
  labels.json           what a correct run does; read by the graders, never shown to an agent
  seed/<name>.json      facts stored in the run's base before the agent starts
  workspaces/<name>/    the files of a workspace; the stand makes it a git repository
  web/<host>/<path>     pages the web tool serves; https://<host>/<path>, a directory serves its index.md
```

### scenarios.json

```json
{"scenarios": [
  {"id": "S01", "kind": "capture", "prompt": "...", "workspace": "alpha", "seed": "base", "chain_from": null}
]}
```

- `id`: `S01`, `S02`, … — opaque, it says nothing about the scenario.
- `kind`: `capture` (the run may learn something worth keeping) or `recall` (the answer may depend on an earlier
  session). A control is either kind with nothing to keep or nothing to recall.
- `prompt`: the user's one message, in English, a natural task that one non-interactive session can finish without
  asking back. It does not mention memory, memcue or the shelf unless the scenario tests an explicit cue (then the
  label says so).
- `workspace`: a directory under `workspaces/`, or `null` for an empty one. The stand runs `git init` in it with the
  remote `https://example.com/acme/<workspace>.git`, so a fact of this repository has the scope
  `repo:example.com/acme/<workspace>`.
- `seed`: a file under `seed/` without `.json`, or `null` for an empty base.
- `chain_from`: for a recall run that must read what an earlier run of the same arm stored itself, the id of that
  capture scenario; the stand runs it in a new session over the base that run left (same arm, same repeat), and
  `seed` must then be `null`. Otherwise `null`.

### seed/<name>.json

```json
[{"content": "...", "keywords": ["...", "..."], "scope": "shelf:research", "kind": "environment",
  "origin": "docs.vellum.example 2026-03-10"}]
```

- `scope`: `global`, `host:<name>`, `service:<name>`, `repo:example.com/acme/<workspace>` or `shelf:research`.
- `kind` (optional): `rule`, `decision`, `pitfall`, `environment`, `procedure`, `event`.
- `origin` (optional): where and when it was learnt, as memcue prints it. A fact cannot be backdated; when its age
  matters, its text says the version or the date.

### web/<host>/<path>

Plain text or Markdown. `https://docs.vellum.example/limits` is `web/docs.vellum.example/limits` or
`web/docs.vellum.example/limits/index.md`. A URL with no file answers 404. The agent has a `web_fetch` tool for these
pages and no other way to reach the network for content.

### labels.json

```json
{"S01": {
  "purpose": "what the scenario tests, one or two sentences",
  "explicit_cue": false,
  "expected_capture": [{"key": "D1", "statement": "...", "place": "shelf"}],
  "must_not_store": ["..."],
  "shelf_needed": false,
  "answer": {"correct_if": "...", "partial_if": "...", "wrong_if": "..."}
}}
```

- `expected_capture`: each finding a correct run stores; `place` is `shelf` (`shelf:research`) or `ordinary` (any
  other scope). Empty when the run should store nothing.
- `must_not_store`: what a correct run leaves out (a claim nothing supports, a document of the repository, task
  progress, a duplicate of a seeded fact).
- `shelf_needed`: whether answering well needs a fact that only a search naming the shelf returns.
- `answer`: how a grader tells a correct, a partial and a wrong final answer; for a capture scenario, about the
  answer to the task itself.
