# #838: OpenClaw Memcue tool plugin

Hypothesis: an additive native tool plugin can offer the full Memcue fact
lifecycle without replacing built-in OpenClaw memory. Baseline: the built-in
memory alone, with no access to the separate Memcue fact store.

Initial adapter experiment: source based on `b8c5849c112dc9c5670bc917ce7b26cf1421a169`,
Node 26, synthetic subprocess. Verify registration, literal argument handling,
explicit local database selection, empty recall vs. failure, and refusal for
conversation, missing audience, sandbox and expired authority. The lightweight
contract passed and `npm pack --dry-run --ignore-scripts` listed the four expected
files. No real CLI or Gateway, model call, or private data was involved.

An additional CI test uses the built CLI with a temporary database, separate tool
contexts and the capture/recall/correct/retire sequence. It does not validate
Gateway invocation or show a recall-quality improvement.

Decision: keep the package experimental. Before deployment, install the tarball
on an isolated Gateway and confirm the owner and group behavior and tool
guidance. Do not connect the production corpus to this prototype.

Follow-up: merged upstream `0b5d2af`. The original base already contained
Hermes conditional-search PR #837; the newer commits added snapshot measurements
and prepared a release. Aligned the prototype descriptions with the conditional
lookup guidance, including unexpected errors and a qualified history fallback.
On OpenClaw 2026.11.10, the packed archive installed into a temporary profile
with explicit capability consent; runtime inspection reported loaded and all four
tools registered. Both adapter tests passed with the checksum-verified Memcue
0.41.0 release binary. Registration and injected-context contract tests are not
end-to-end proof of live session admission; that remains pending.
