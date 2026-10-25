---
name: memcue
description: Write to long-term memory (memcue). Use when the user asks to remember, note or keep something for later ("запомни", "remember this"), to forget it or says a stored fact is wrong or outdated, or when a fact found with memcue search turns out wrong in this session.
---

# memcue: writing to memory

Memory is a base of atomic facts. You read it with `memcue search "<words>"`, as the session-start note says; facts
are extracted from every session automatically when it is compacted or ends. Write by hand only what the user asks for
and corrections; the session extraction covers the rest.

## Remember

```bash
memcue add -keywords "word1, word2, синоним" -origin "<repository> <YYYY-MM-DD>" "<fact>"
```

- One statement in one to three sentences; a rule goes with its reason.
- Self-contained: name the system, host, repository, path. No "it", "this project", "above".
- Exact values byte for byte: paths, commands with flags, versions, ports.
- Only what the user said or a command confirmed; no guesses.
- Keywords: 3–8 words someone would ask with, including synonyms and the other language.
- Origin: the repository directory name and today's date.
- Session card: add `-kind rule -scope global` for a rule the user stated for every project, and
  `-kind decision -scope repo:<name>` for a choice of this repository. The card carries exactly these two combinations,
  newest first, so a durable rule or decision reaches the agent even when a prompt shares no word with it.
- Other values: `-kind pitfall` or `-kind environment`; `-scope host:<name>` or `-scope service:<name>`.

## Correct or forget

Find the fact first; `search` prints IDs, best match first. `-in all` looks in every repository: by default search
leaves out facts bound to other repositories, and `-expired` adds facts whose expiry has passed:

```bash
memcue search -in all "<words of the fact>"
memcue correct ID "<corrected fact>"   # new version; keywords stay unless -keywords is given
memcue retire ID                       # no longer recalled; the history keeps it
memcue reopen ID                       # undo a retire made by mistake
```

Correct when the fact changed; retire when it no longer holds and nothing replaces it. Tell the user which fact you
changed.
