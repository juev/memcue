# Two initial synthetic native route-smoke episodes

This checkpoint executed the two predeclared initial episodes: SQLite-content
and PG-hybrid-rrf60. Each used `gpt-6.1-sol`, high effort, the same OpenAI native
provider/configuration, fixed instructions and three invented facts. The model
chose its query, received a genuine backend result and produced its final answer.
Neither episode was resubmitted. This is route smoke, not natural-task relevance,
comparative effectiveness or statistically representative latency evidence.

| Initial episode | Submitted turns | Executed searches | Elapsed seconds | Native input/output tokens | Final fact |
| --- | ---: | ---: | ---: | --- | --- |
| SQLite-content | 1 | 1 | 6.167 | 1205 / 55 | MRG-4821, correct |
| PG-hybrid-rrf60 | 1 | 1 | 9.211 | 1205 / 49 | MRG-4821, correct |

Both returned all three declared version-1 facts, including the negation and
harmful distractor. Author inspection of the actual final sentences found that
both explicitly named MRG-4821 as approved, endorsed neither rejected code, and
gave no advice to disable safety checks. This judgment uses the answer meaning
and fact content, not just a target ID; it is not independent judging.

**Both episodes remain technically ineligible because budget enforcement units
are UNKNOWN.** Actual app-server usage notifications provide token counts but
omit provider `codex_rollout_budget_units`. Their absence in the 0.166.0 projection
does not prove that the provider omitted an override. No weighted-token fallback
compliance claim is made. The native completed-response threshold was configured
at 4096; it can overshoot and is not an output-token or billing cap. Observed
output counts were below 4096, while enforcement-unit overshoot remains unknown.

Both episodes finished before the external 60-second deadline; no actual episode
needed interruption. Native processes exited 0 and their groups were absent.
Controlled tests exercised no-event and blocked-backend deadlines, bounded
termination, nonzero/malformed subprocess results, malformed events followed by
later answers, unexpected tools, duplicate calls and N+1 refusal. A fourth search
never executes. Those tests do not prove exact provider cancellation or billing
cessation at a deadline. The controller records that uncertainty explicitly.

The task-local Python 3.13 environment installed all dependencies with a generated
hash lock constrained by the existing requirements lock. All 26 installed
transitive versions matched constraints; installed distribution record trees are
hashed. Psycopg 3.2.13 used its genuine bundled libpq (170006), authenticated the
existing app role via libpq's private passfile, and passed SELECT 1. The prior
system-library discovery result is preserved and was not an endpoint failure.
No host package install, new privilege, database or extension provisioning occurred.
No selected-wheel archive hash is invented; installed trees and permitted resolver
wheel hashes are the retained distribution provenance.

Unchanged multilingual E5-small weights at revision
`614241f622f53c4eeff9890bdc4f31cfecc418b3` were loaded locally and hash-verified.
No model download or substitution occurred. A fresh absent owned PG prefix held
three facts and vectors, with exact new-target read-back. Only its tables and
catalog metadata were accessed; prior user relation data and sentinel were not
read. No prior relation was changed. SQLite used the genuine native bridge and
content-only index. The existing bridge also constructed an unused full-index
cache, with zero baseline queries; it was not another episode or comparison.

An initial setup failed before PG writes because empty synthetic `source` metadata
triggered the native store's version migration. That fixture remains archived.
Before inference, a retained declaration revision supplied native default
`source=agent` identically across arms; contents/IDs/instructions/model stayed the
same, and the successful histories matched exactly at version 1.

The installed CLI rejects `--ignore-user-config`. The supported route reused its
existing native home with an empty workdir and explicit isolation configuration.
Fresh credential-free captures passed the accepted strict full-text/tool gate,
including the chosen existing-proxy environment used identically for both arms.
Only endpoint/provider auth metadata and thread provider selection changed for
authenticated execution. The four-field catalog capability override remains
qualified. Native account metadata was checked with refresh disabled; the
controller never opened/copied OAuth or passfile contents, logged HTTP headers,
or retained proxy values or native credential diagnostics. Authenticated HTTP
bodies were not intercepted: their correspondence is through captured runtime
configuration and actual native RPC/events, not an additional wire-body claim.

All 24 relevant native behavioral/config tests passed. Executed sources, argv,
exits, configuration differences, actual queries/results/finals, usage, SQL owned
identities/read-back and hashes are retained privately. Only owned fresh SQLite
fixtures were archive/hash/link-verified and removed. Historical packets,
archives, production/module defaults and accepted isolation source are unchanged.
The next seam is fresh independent review of the dispatcher and initial pair,
including the unresolved enforcement-unit visibility. Any later model repetition
requires its own declaration; this checkpoint authorizes no silent retry. Parent
owns review/CI/publication/acceptance. Full #674 remains open.

## Later backend-lifetime erratum

Independent review rejected candidate `b2314cf9` for L1: an exited backend group
leader could leave a descendant holding pipes, and waiting for a leader after
TERM did not establish group termination. This later source repair holds the
unreaped leader as an identity/pgid anchor, uses bounded non-reaping pipe I/O,
signals only its verified private session/group, and reaps adopted descendants
with a scoped Linux subreaper. Cleanup grace cannot publish a result after the
external deadline. Local fork fixtures exercise exited and TERM-exiting leaders,
TERM-ignoring descendants, nonzero errors, bounded cleanup and unrelated-group
safety. These are local process tests, not a new model episode or retroactive
exercise of the recorded pair under the repaired source.

The original measured artifacts remain unchanged: two real turns/searches,
both native episodes exited 0 and both answers were independently judged correct;
both remain budget UNKNOWN/ineligible. The prior outer invocation exited **124
after finalization**, so no outer-success or `turn.completed` receipt is claimed.
Historical hashes are trusted baselines rather than a reason to rescan private
or credential trees. Parent fresh independent re-review is required.
