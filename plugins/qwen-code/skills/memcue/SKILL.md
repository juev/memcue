---
name: memcue
description: Search and write long-term memory when the user explicitly states a lasting preference or asks to remember or forget something, or when a verified finding would change what a later session does and is not already kept in the session or repository docs.
---

# Write memory with the existing CLI

Use the configured launcher from the SessionStart note or QWEN.md for **every** command below. Append the
arguments shown to that launcher; do not call an unconfigured `memcue` binary. If the note is missing, ask for the
configured launcher or read the integration setup. A hook's environment does not carry into your shell.

Run from the session's current working directory, including a nested or non-Git directory. Search first:

```text
search -json -in all "<words of the fact>"
add -kind rule -scope global -source user -- "<fact>"
correct -expected-version VERSION ID "<corrected fact>"
retire -expected-version VERSION ID
reopen ID
```

Write new and corrected fact content in English; search in English first, then synonyms, exact identifiers and
legacy-language words if needed. Preserve identifiers, commands, paths, versions, exact values and verbatim quotations.
Keep user-facing replies in the requested language. The store accepts other languages; this policy does not translate data.
Never correct a fact solely to change its language. Existing fact content remains as stored.

Read the actual search object: `facts` is an array, `hints` is optional. A fact carries `id`, `content`, optional
`origin`, `scope`, `expires`, and `created`; it has no `source` or `kind` field in search JSON.
Inspect the configured base history when persisted provenance is needed. Correct the matching fact instead of adding a duplicate.
`correct` preserves source, kind, scope and expiry; `-expires YYYY-MM-DD` changes expiry,
and `-no-expires` removes it. `retire` keeps the history; `reopen` undoes retirement. Tell the user what changed.
Correct or retire a stored fact only when the user says it is wrong or a command or a file of this session shows it:
a newer fact that says otherwise is not that evidence on its own. When nothing in the session settles it, answer from
the facts and their days, name the conflict, and ask before you correct or retire one.

Choose metadata deliberately on add:

- User-stated lasting rules: `-kind rule -source user -scope global`, or `-scope repo` when limited to this project.
- User decisions: `-kind decision -source user -scope repo` only when explicitly stated by the user or they ask you to remember it.
- Verified pitfalls: `-kind pitfall -source tool`; keep cause and workaround if a later session would hit the failure again.
- External environment: `-kind environment -source tool -scope host:NAME` or `-scope service:NAME` as appropriate.
- Procedures: `-kind procedure` only when the user asks to remember them.
- Temporary events: `-kind event`, with `-expires YYYY-MM-DD` when the default seven days is unsuitable.
- Agent conclusions: `-source agent`, clearly distinguished from observed evidence and user instructions.

A pitfall requires reproducible environmental, platform, tool, or repository behavior, a verified cause and fix or workaround, and relevance to a later session in a fresh checkout. Do not store typos or invocations resolved by the tool's own help; transient network/authentication or CI-runner state; a defect the current task just fixed; finished experiments, rejected candidates, release notes, or one-off observations; obsolete migration/install state; or unverified and context-free fragments. Design decisions, durable host configuration, and external research limitations remain capturable under their appropriate kind, scope, or shelf.

Agent/tool pitfall and environment facts expire after 30 days by default. Use `-expires YYYY-MM-DD` for a different review date, or `-no-expires` only for deliberately durable knowledge such as repeatable platform behavior.

Keep each fact self-contained and atomic. Name the system and useful search words. Do not store credentials, task
progress, what the repository shows, installation state or unrequested procedures. Default origin comes from the
current cwd and date. `-scope repo` requires a project identity; a temporary non-Git directory has none. Use an explicit
scope when no project exists; do not invent a repository. `-origin` is for deliberately naming another origin.

## When search finds nothing

The agent of an earlier session may have forgotten to store what you need, and it is often still in that session's
transcript. Run `sessions "<words>"` with the words of the empty search, through the same launcher: it reads the text
of this machine's earlier sessions, with no index, and prints the newest passages that hold the words, each with its
date and session. Pass `-session <current session id>` to leave the current session out. A passage is history, not a
fact: check it against the current state, and store what holds with `add` so the next session finds it by search.

## Findings from outside sources: the research shelf

For outside research, search `search -json -in shelf:research "<words>"` first. Then use:

```text
add -shelf research -source tool -- "<finding>. Source: <document, version, or date>."
```

Ordinary search excludes shelves. Store what the source says and its limits, mark inference, and compare version/date
before relying on an old finding. Correct or retire stale findings. A repository document belongs in the repository.

On a failure or denied write, report the diagnostic and continue unrelated work. Inspect persisted state after a timeout
before retrying a write. Request access only to the configured base and state directories when the sandbox needs it.

Read `version` from the earlier `search -json` result and pass that caller-observed version to correct/retire. On conflict, read again and make a new decision; never fetch latest immediately before writing to conceal a stale decision, and never replay automatically. PostgreSQL requires the version; old local CLI calls remain compatible.
