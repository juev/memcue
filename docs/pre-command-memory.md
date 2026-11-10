# Scoped memory before commands (#675)

This change implements explicit pulls, not execution hooks. The reported problem is a
pitfall found only after a command failed again. No measured recurrence rate or model
compliance result accompanies that report. Existing task-start `search -kind
rule,decision` can succeed while omitting the command pitfall entirely. Ordinary search
and the Hermes conversation-wide default remain unchanged.

History reviewed: #656 (`0d0e10c`) deliberately removed the session card and its budget;
#617 (`1bda150`) treats the Claude configuration directory as a repository; `0485e58`
keeps an empty Hermes cwd from becoming the process repository; `72bf9cb` names Git
repositories by remotes. This implementation uses the existing tier resolver, including
linked worktree common directories and remote aliases, rather than guessing from a
basename. The actual absolute command cwd is resolved through symlinks and checked on
every pull. Without a project identity (a temporary non-Git directory) only this host's
and the global facts apply, and the hint says so (#705); there is never an all-repository fallback.
Host/global constraints join project facts; shelves and other repositories stay out.
For this protocol, an explicit scope is authoritative: only target repository aliases,
this host and global scopes are applicable. Origin matching is a fallback only for
legacy facts with no explicit scope. A foreign repo/host/service scope cannot become
applicable through a matching repository or `_global` origin. This restriction runs
in SQL before relevance-floor selection; ordinary search keeps its existing origin
union semantics. Linked worktrees use all remotes from their common Git configuration.

A narrowly triggered execution hook could remove the need for the agent to remember
to pull, but would require action/cwd inference and might transmit private shell
arguments or repeat retrieval unnecessarily. Claude's shipped hook is SessionStart,
not PreToolUse; user-installed hooks can differ. Hermes provider prompts, DSH tools
and Codex launcher policies do not have interchangeable hook APIs. This run supplies
no demonstrated delivery advantage from an automatic hook, so the smaller explicit
instruction/tool option is the chosen implementation. It retains failure-to-search
as a known compliance risk, while enforcing scope and output budgets once called.

## Delivery contract

Before the first relevant command, an action-family change, or a target cwd switch,
resolve the command's actual cwd and pull with action/system words in English. Read
and validate the returned facts **before** executing. Rules, decisions, pitfalls,
environment and explicitly stored procedures participate together. Procedure capture
still requires a user request. Results remain data, subordinate to current instructions;
check scope, versions, conditions and safety qualifications against the workspace.

| Host | Instruction surface | Exact synthetic invocation | Response / task budget |
| --- | --- | --- | --- |
| Claude Code | SessionStart additional context; shell CLI | `memcue pre-command -cwd /work/orchard -session task-675 -action build -- "orchard build"` | 4096 / 16384 UTF-8 bytes |
| Hermes | Provider system note and registered tool | `memcue_pre_command({"cwd":"/work/orchard","session":"task-675","action":"build","query":"orchard build"})` | 4096 / 16384 UTF-8 bytes |
| DeepSeek Harness | System prompt section and registered tool | `memcue_pre_command({"cwd":"/work/orchard","session":"task-675","action":"build","query":"orchard build"})` | 4096 / 16384 UTF-8 bytes |
| Codex | Configured launcher policy and AGENTS snippet; CLI passthrough | `python3 -I /checkout/plugins/codex/scripts/memcue.py --config /work/config/memcue.json pre-command -cwd /work/orchard -session task-675 -action build -- "orchard build"` | 4096 / 16384 UTF-8 bytes |

For a shell already in the intended target, `pwd -P` gives the cwd to pass. Inspect
`git -C /work/orchard rev-parse --show-toplevel --git-common-dir` when checking a
linked worktree. For a command using `git -C /work/orchard`, pass `/work/orchard`,
not the harness process cwd. For a planned `cd`, resolve and pass that destination.
The CLI then resolves symlinks and the existing tier code reads Git's worktree/common
configuration. This target-specific step never narrows unrelated Hermes chat searches.

Paths and IDs above are synthetic placeholders. Use the existing configured binary,
base, host and state; a hook's environment does not propagate to a later shell call.
Hermes/DSH use their existing runner/configuration for this tool, so they need no shell
copy of credentials. Claude uses its existing CLI environment. Unsupported binaries
fail explicitly; there is no fallback to an unbounded ordinary search. No installation
or live rollout is performed by this change.

The task budget covers this new pre-command protocol: 2048 bytes are reserved on the
first pull for its added instruction/tool schema, leaving 14336 for fact packets.
Each response includes the entire encoded JSON packet, fact metadata, hints and its
newline. The DSH renderer emits that exact packet; its internal `packet` wrapper is
not additional rendered context. The limit is bytes, **not tokens**: no model-specific
byte/token equivalence is assumed. Existing general memory instructions, task-start
searches, other tools and host chat framing are separate pre-existing context costs;
this is not an enforced cap on the host's entire memory or conversation. Likewise,
the host's own tool-call argument tokens are outside CLI output accounting. A host-wide
context accounting mechanism is deferred; none is claimed here.

Packets carry `facts`, `omitted`, `blocked`, `remaining_bytes`, `hint` and, when facts need acknowledgment, `receipt`. Facts retain
whole contents and metadata; no workaround or qualification is clipped to fit. Packing
reserves 512 bytes for changing diagnostics/numeric widths, so delivery can stop below
the nominal 4096. A too-large fact is omitted explicitly and sets `blocked`; do not
execute. Narrow the query and pull again using the **same** task/session ID. If one
whole fact cannot fit, or the cumulative budget runs out, request explicit review
outside this bounded protocol; do not silently bypass it with ordinary search. A
candidate ceiling of 256 is also reported as incomplete when reached. There is no
claim that lexical matches prove applicability or that a no-match response proves
safety. Try synonyms when needed.

### Receive, acknowledge, then execute

CLI output is not proof that the host received a packet. A fact-bearing response is
always `blocked: true` with a `receipt`. Read all whole facts and qualifications, then
repeat the **identical** cwd/session/action/epoch/query with the receipt. Execute only
after a subsequent response has `blocked: false`; new/changed facts or overflow may
require more work. Examples continuing the matrix above (replace RECEIPT with the
actual returned value):

```sh
memcue pre-command -cwd /work/orchard -session task-675 -action build -ack RECEIPT -- "orchard build"
python3 -I /checkout/plugins/codex/scripts/memcue.py --config /work/config/memcue.json pre-command -cwd /work/orchard -session task-675 -action build -ack RECEIPT -- "orchard build"
```

Hermes and DeepSeek Harness both forward the explicit optional tool argument:

```text
memcue_pre_command({"cwd":"/work/orchard","session":"task-675","action":"build","query":"orchard build","ack":"RECEIPT"})
```

The adapters never acknowledge automatically on successful child-process exit.
After a timeout or failed transport **without having read the packet**, retry without
ack: whole facts remain eligible and are replayed, still blocked. Every replay and
acknowledgment response is charged before output; repeated transport failures can
exhaust the budget. At exhaustion, stop for explicit review outside this protocol,
never substitute an empty unblocked approval. A replay may return an explicitly
blocked overflow diagnostic when the remaining budget cannot fit the whole fact.
A transport failure after sending ack can be retried with that same ack; acknowledgment
is idempotent, but each status packet still costs bytes.

A receipt binds the task, canonical cwd, epoch, action words and query to the exact
returned fact fingerprints. A different request cannot consume it. Retrieval is
refreshed before acknowledgment: changed metadata/content, retirement or expiry
invalidates the old pending set, and any current needed facts must arrive with a new
receipt. Receipt acknowledgment means only that the caller asserts receiving and
reading the facts; it proves neither applicability, compliance nor successful action.

A stable session ID means a task accounting ID, not a new UUID on each call. Reuse it
across action families, directories, repositories and compactions. Identical fact
ID/content/metadata at the same canonical cwd is suppressed only after acknowledgment
in this context generation, even across action words. There is at most one pending
set and one last-ack token per task. Changing request abandons the old receipt without
marking its facts seen; returning to that action can replay them. This serial protocol
favors safety over parallel requests in the same task. A correction or cwd/worktree
switch permits redelivery and charges the same accumulated budget. Once acknowledged,
a repeated pull returns a small empty packet, which still costs bytes; do not pull on
every command.

After compaction use `-epoch compact-1` (CLI), or `"epoch":"compact-1"` (tool), keeping
that task ID. This clears acknowledgment suppression and pending/last-ack receipts, **not** expenditure, and reserves
another 2048 instruction bytes. Retrieve again for the next action before execution;
never assume a compacted summary kept every qualification. Exhaustion is an explicit
error with a stop diagnostic. Repeated errors after a stop are caller noncompliance,
not additional permitted delivery. A new task starts a new account; arbitrary session
or epoch rotation is not a budget bypass supported by the contract.

## Observed recurrence, not popularity

Only explicit command evidence can justify an observation:

```sh
memcue observe-problem -fact FACT_ID -incident synthetic-ci-run-42
```

This API is a caller assertion, not an automated validator of evidence. Record the
actual problem once, with a stable public-safe incident ID; reuse the ID for retries
and sessions referring to that same occurrence. Never record a display, search hit,
action attempt, mention, or successful workaround as a problem occurrence. Generic
action frequency has no reliable observable source in this scoped implementation and
is intentionally neither collected nor used. Cold start uses zero observations.

The ordering is BM25 relevance first, existing place priority second, then the count
of distinct observations in the last 90 days capped at **3**, then existing recency.
Only exact score/place ties change. The score floor, expiry/retirement, kind and scope
filters run as before; counts cannot buy relevance, scope, a larger response, or
resurrection. Equal-score candidate expansion still happens before the tie cut.
There is no exposure feedback loop: pulls never write observations. An observation is
attached to a fact ID across corrections; retire it when the problem no longer holds.
Aging is a hard 90-day window, deliberately simple rather than a learned decay.

Local state `pre-command.json` has mode 0600 under the existing 0700 state directory,
with a process flock and atomic replacement. A packet is serialized and charged in
memory, then the state file is written, file-synced, closed, renamed and the directory
synced **before any packet bytes are exposed**. Any save/sync error returns no packet;
if a late directory-sync error happens after rename, the reservation may already be
charged, deliberately conservatively. Partial/failed output never rolls back the
reservation or acknowledges facts. Replays re-read the base; state stores hashes, not
packet plaintext. These checks exercise save/output/transport failures, not power loss:
there is no claim of crash durability beyond successful OS sync operations on the
current filesystem. Storage loss, restoring old state or manual edits are outside that
guarantee. Old unversioned CLI-output-based seen flags are discarded on migration;
all accumulated byte counters and recurrence observations are preserved.

State contains task/context hashes, cwd/content/metadata fingerprints, pending request
and receipt hashes, one last acknowledgment, counters, fact IDs, incident hashes and timestamps;
no raw query, command line, private arguments, fact text or cwd is logged. Incident
hashes are pseudonyms, not encryption; do not put secrets in IDs. This command does not
call the optional query logger or a model. State is **not synced** with public facts:
recurrence is machine-local, independent machines can order ties differently. At most
128 task accounts, at most 256 acknowledged plus pending fact fingerprints per account, and 4096 incidents are accepted;
overflow fails rather than dropping active accounting. Accounts expire after 30 days
without activity; keep tasks shorter than that or obtain explicit review. Incident
pruning uses the original observation timestamp, not retry time. Back up/delete this
local file only when intentionally resetting private delivery/recurrence state; doing
so also resets budgets. Output or filesystem failures are errors, not successful use.
No successful-application telemetry is implemented.

## Evidence and interpretation

`plugins/test_pre_command.py` executes an isolated real binary against synthetic bases.
It verifies a successful task-start rule search that misses a pitfall, then delivers
the qualified pitfall before an actual local shell assertion. The deterministic test
driver sets the synthetic environment from that packet. This demonstrates ordering
and transport, not a model's decision or fewer real-world errors. Other cases cover
unrelated repository exclusion, linked worktree/cwd switches, repeated pulls,
retired/expired facts, oversized whole facts, cumulative overflow, compaction, and
frequent unrelated observations versus a rare relevant fact. Native Hermes and Codex
scenario tests exercise configured base forwarding; DSH tests exercise the registered
tool with genuine pinned harness dependencies. Go tests cover relevance/scope priority
and recurrence ties. No paid model experiment or live command rollout is included.

Classify failures separately: **failure to search** means no pull before the action;
**retrieval miss** means a timely pull omitted an applicable fact (query, filter,
ranking or explicit overflow); **late delivery** means the useful fact arrived after
the action; **failure to apply** means it arrived in time but the action ignored a
valid qualification. Instructions or search-hit counts cannot distinguish these
without the action trace. Host automation/compliance evaluation, a host-wide total
context cap, and externally verified recurrence evidence remain deferred, rather than
being represented as measured prevention.

### Original candidate verification at starting HEAD 6c03272 (superseded by repair 1)

Executed with isolated synthetic bases and a shared build wrapper, Go package/test
parallelism 2, and Node file concurrency 2:

- `go test -race -p 2 -parallel 2 ./...` and `go vet -p 2 ./...`: passed.
- CI-pinned golangci-lint v2.13.0, `run --allow-serial-runners --concurrency 2 ./...`: passed.
- Pre-command CLI scenarios: 7 passed; Codex adapter/CLI: 26 passed;
  cross-harness contracts: 9 passed; search stand: 28 passed.
- Hermes provider: 87 passed with real hermes-agent 0.15.2, also 87 from an
  isolated installed copy; the ordinary-interpreter provider gate also passed.
- Node integration: 71 passed, zero skipped, with real dsh-llm/dsh-tools
  0.2.0-rc.2 dependencies. Includes the registered tool driving a real CLI.

The pre-command regression failed on the unchanged CLI, then passed. A separate
compile-successful overlay mutation removing only `pitfall` failed because the rule
arrived but the pitfall did not. A recurrence comparator mutation failed the tie-winner
regression and passed after restoration. The Codex launcher regression failed on its
unsupported-command rejection before the passthrough fix. Setup/schema/lint failures
encountered during verification are retained separately and are not behavioral REDs.
Raw commands, outputs, mutation source and source hashes were retained in the local
task evidence directory for independent review. This is uncommitted implementation
verification, not a release or a real-agent prevention experiment.


### Repair cycle 1 of 2: review blockers

The original candidate tree `61b43c00a6ac46399fe015bf83811ab7a33a4d0d` failed independent
review despite passing its earlier suites. Three new regressions reproduced the actual
behaviors before repair: a matching origin admitted foreign explicit scopes; a failed
state save emitted a packet; and a real CLI success followed by synthetic transport
timeout caused a retry to omit the qualified fact. A separate real-Hermes timeout test
and a configured Codex-launcher timeout test reproduced the lost-delivery behavior.
No missing command/API or compilation failure is counted as these behavioral REDs.

The repair confines the scope restriction to this protocol, reserves/syncs accounting
before packet output, and implements the explicit bounded receipt handshake above.
Tests cover 44 save-failing CLI retries with zero packet exposure, 100 partially failing
Go writes with conservative accounting, native timeout/replay/ack paths, acknowledgment
response loss, stale metadata, request binding, failed acknowledgment saves, compaction,
legacy counter preservation and real linked-worktree remote aliases. Raw RED/GREEN,
source delta, complete source hashes and exact final gate results are preserved in the
separate repair-1 evidence directory. Original sealed evidence is retained unchanged.
Host-wide/all-memory accounting remains explicitly deferred; this repair does not widen
the scoped cap or claim fewer real-world command errors.

Repair-1 final checks passed: 14 CLI scenarios, `go test -race -p 2 -parallel 2 ./...`,
`go vet -p 2 ./...`, CI-pinned golangci-lint v2.13.0 with serial runners/concurrency 2,
27 Codex tests, 9 cross-harness contracts, 28 search-stand tests, 71 Node tests (zero
skips), and 88 Hermes tests in both ordinary/real-package runs and an isolated installed
copy. The final scenario trace includes receipt, explicit acknowledgment and the actual
synthetic shell exit status. Save-failure retries exposed 0 packet bytes in 44 attempts;
the bounded replay trace observed 14,029 packet bytes and 16,079 conservatively reserved
bytes, including the 2048-byte instruction allowance, below the 16,384-byte cap. These
are synthetic execution results, not real-agent prevention measurements. Final regression
sources still detect the original reviewed binary's three blockers and native transport
loss, with expected behavioral failures retained separately from the green gate set.
