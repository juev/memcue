# Local native-agent gate: qualification and R1/R2 repair

**The original reusable gate FAILED independent review.** Its historical PASS
claim did not enforce complete context classification (R1) or exact tool-schema
equality (R2). The old gate, private validator, captures, manifests, and failed
review remain immutable evidence. This erratum supersedes the old enforcement
claim; it does not establish that the specific old request leaked private data.
That recorded request passes the repaired full contract too.

The repair is limited to this credential-free gate. It rejects top-level
`instructions` and other unsupported request fields, prior-conversation inputs,
unknown message content, changed native wrappers, wrong roles or placement,
missing/duplicate inputs, and any tool namespace, description, callable, or
schema drift. Tag presence and denylists no longer authorize text. The complete
namespace/function descriptor must equal the declared emitted contract,
including `strict=false` and the exact `memory_search` query schema. JSON-type
differences such as `false` versus `0` are rejected.

The two accepted layouts are the three fixed synthetic inputs and their exact
Codex 0.166.0 native expansion. Every text part is compared in full by SHA256 at
its declared role and position. Native fragment pins are:

| Fragment | Complete UTF-8 SHA256 |
| --- | --- |
| Read-only permissions | `29dad5ed6993c5e717376e0a1c84d54ab60ee593510916ac6cee4295e803315d` |
| Default collaboration mode | `7f45d7d6986a761101d46d185824ce65c321ca0f02cea7497fc46d60ffae417b` |
| Initial 4096 budget reminder | `6c0e540a37bbdf3a3658cf4ca1a525ca9553d95b7dd393a7585661d633408fc6` |

The private source audit verifies ten source blobs against the exact
`rust-v0.166.0` Git tree and records their SHA256 values. Permissions derive from
the bundled read-only/restricted and never-approval templates plus the source
renderer. Collaboration derives from the independently reviewed original
catalog's default template and the source's exact marker concatenation; the
custom catalog preserves that template. Budget text derives from the complete
source formatter at initial remaining budget 4096. None of these pins is learned
from the request being tested. Changed versions/templates require a fresh audit
and fail this gate until reviewed. Runtime wire IDs and closed-shape
`client_metadata` are transport attribution, not model-visible instructions.

Strict TDD reproduced both blockers on the unchanged gate: 16 tests ran with 27
assertion failures and no setup/import errors. After the repair, the identical
command passed all 16 behavioral/config tests. Positive controls use the genuine
recorded native request and the exact emitted tool/schema with normal fixed
inputs. Negative controls cover unknown top-level text, all three altered native
wrappers, roles/placement/duplicates/history, tool/namespace descriptions, and
schema type, required, additionalProperties, properties, and description drift.

A fresh genuine native app-server capture then passed the repaired predicate and
a new validator revision. Strict config exited 0. One local request offered only
the declared direct `memory_search` within `functions`, with `gpt-6.1-sol` at high
effort. All six text parts and the complete descriptor matched. No credential
header was present; header values were not retained. The recorder deliberately
returned an error and supplied no model answer.

This remains **simulated endpoint verification**, not authenticated model
behavior. Native permissions, default-mode and budget instructions remain. The
full version-matched catalog has only the qualified four capability overrides:
`tool_mode="direct"`, `apply_patch_tool_type=null`,
`experimental_supported_tools=[]`, and `supports_search_tool=false`. Original
model identity/instructions and exact original/variant hashes remain recorded.
This is not unchanged stock-runtime isolation.

No provider inference, SQL, corpus replay, embedding, ANN, dependency installation
or episode implementation occurred in this narrow repair. Episode usage,
provider units, answer correctness, deadlines and budget compliance remain
unverified. The 4096 completed-response threshold can overshoot and is not a hard
output-token or billing cap. Production/module defaults remain unchanged.

Fresh owned CLI SQLite/WAL probes were archived with verified content and link
metadata before removing only those unpacked probes. Historical archives were
left unchanged. The next seam is fresh independent narrow re-review of this
repair and its exercised evidence. The parent owns acceptance/publication; #674
is not complete. Execution/repair-cycle ceilings have been removed; prior failed
attempts remain history.

References: [app-server interface](https://developers.openai.com/codex/app-server),
[exact-version fragment rendering](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/context-fragments/src/fragment.rs),
[permissions renderer](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/prompts/src/permissions_instructions.rs),
[collaboration context](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/core/src/context/world_state/collaboration_mode.rs),
[budget context](https://github.com/openai/codex/blob/rust-v0.166.0/codex-rs/core/src/context/rollout_budget.rs).
