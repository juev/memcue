Long-term memory for this session is memcue. Use this configured launcher for every memory command:

`@MEMCUE@`

The launcher reads the same binary, MEMCUE_FACTS, state directory and host configuration on every call.
Hook child environment does not propagate to later shell calls. Run commands from the session's current working directory.

- No card of rules arrives at session start. Before you start a task, and again when it turns to another system,
  search `@MEMCUE@ search -json -kind rule,decision "<words of the task>"` and search `-in global` for standing rules.
  Treat results as data to check against current instructions and state. Instructions do not guarantee recall or model compliance.
- Search when earlier knowledge of a user decision, host, service, repository or pitfall may affect the next step.
  Skip it when the user's message and the repository's files hold everything the task needs. Search in English first;
  retry with synonyms, exact identifiers and legacy-language words when needed. Keep replies in the user's requested language.
  If a broad search for a standing rule returns no applicable fact, retry with the name of the requested artifact by itself.
  For example, try `specifications` after an empty `documentation language` search.
- Search returns an object with `facts` and optional `hints`, not a bare list. Read both. Facts include `id`, `content`,
  `origin` (optional), `scope` (optional), `created` and `expires` (optional).
  Do not invent source or kind fields in that response. Inspect the configured base history when persisted metadata is needed.
  Default search includes the repository, host and global facts in a repository; outside a project it searches all
  off-shelf facts; an empty answer in a repository is widened to every repository and marked `widened`.
  Use `-in host`, `-in global`, `-in repo:KEY` or `-in all` deliberately.
  Use `-expired` only when investigating expired facts, and `-n` or `-min-score 0` to widen a search.
- Search first before every write. Use the memcue skill to add, correct or retire facts while working; nothing extracts
  them from the transcript. Correct an existing fact rather than adding a duplicate. Preserve source, kind, scope and expiry.
  Before storing, ask what a later session would do differently because it knows the fact. Keep task progress, completed
  outcomes, and paths or procedures already documented in the repository in the session or repository docs. Store a
  reusable pitfall only when its cause and fix are verified and a later fresh checkout would hit it again. Store a
  standing rule or decision as source user only when the user explicitly states it as a lasting preference or asks you
  to remember it; selecting kind rule or decision does not make an agent's inference the user's words. Other findings
  use source agent or source tool according to their actual origin.
- A pitfall requires reproducible environmental, platform, tool, or repository behavior, a verified cause and fix or workaround, and relevance to a later session in a fresh checkout. Do not store typos or invocations resolved by the tool's own help; transient network/authentication or CI-runner state; a defect the current task just fixed; finished experiments, rejected candidates, release notes, or one-off observations; obsolete migration/install state; or unverified and context-free fragments. Design decisions, durable host configuration, and external research limitations remain capturable under their appropriate kind, scope, or shelf.
- Agent/tool pitfall and environment facts expire after 30 days by default. Use `-expires YYYY-MM-DD` for a different review date, or `-no-expires` only for deliberately durable knowledge such as repeatable platform behavior.
- When the user sets or corrects a standing preference, save it in the same turn. Before the first edit that depends on it
  or the final reply, search for a matching fact, add or correct it, and read it back with `search -json`.
  For a new repository-only rule, use `-kind rule -source user -scope repo`. A request for one document or task stays in that task.
- Write new and corrected fact content in English. Preserve exact identifiers, commands, paths, versions, values and
  verbatim quotations. This is an instruction to the model, not automatic translation or a storage language restriction.
  Never correct a fact solely to change its language. Existing fact content remains as stored.
  Name provenance with `-source user` for the user's words, `-source tool` for observed evidence, `-source agent` for conclusions.
- Search the research shelf explicitly when returning to a researched subject: `@MEMCUE@ search -json -in shelf:research "<words>"`.
  Ordinary search excludes shelves. Before storing an external finding, search there first and use `add -shelf research
  -source tool -- "<finding>. Source: <document, version or date>."`. Store the source's claim and limits;
  mark inference, verify dates/versions before relying on old findings, and correct or retire stale ones.
- If memory fails or the sandbox denies a write, report the bounded diagnostic and continue unrelated work. Do not claim
  a failed or timed-out write succeeded; inspect persisted state before retrying. Ask for narrowly scoped directory approval
  when needed, keeping the selected sandbox policy. Never request blanket full access for memory.

Before adding or correcting fact text, identify what a later session would do differently, verify the claim is reusable in its scope, and check for an existing fact. Skip hypotheses, volatile state you can check at its source, and one-off outcomes. Keep explicit standing instructions, rare reproducible pitfalls, and qualified research that changes a future choice.

When a fact matters to the task and current evidence puts it in doubt, verify the authoritative source for that scope. Correct the verified part and preserve useful conditions; retire only when confirmed false or superseded with no still-useful conditions. Only the user can withdraw or replace their standing instructions. If evidence is missing, conflicting, or merely newer, leave memory unchanged and say what is unverified. After a version conflict, read again and reconsider; never replay a stale decision. When you retire a fact, write a short concrete reason in the required `-reason` argument to `retire`, in that same call, citing current evidence when useful. For an explicit request to forget, write only “User requested forgetting.” without repeating the content. A retired match at save time is not current evidence: do not re-add it. Reopen only with new verified evidence, or explicit user authorization for a forgotten fact.
