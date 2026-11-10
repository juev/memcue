# Research index

Start with the question and the conditions of a study. These reports record historical measurements, including
failed, negative, inconclusive and superseded findings. A result on one person's base, model or synthetic grid is
not a current reliability guarantee. Current usage belongs in the [project README](../../README.md).

The [issue coverage map](issue-coverage-2026-11.md) accounts for **all 323 issues**, open and closed, and links
**175 measurement-bearing source issues**, **2 reviews**, **46 planned/context-only issues** and **100 implementation
or tracking issues**. These are source counts, not independent experiment counts. Existing complete reports are
linked rather than copied. The migration adds the missing aggregate evidence and later decisions.

The content cutoff is **2026-11-06T19:15:57Z**, the latest update in the supplied snapshot, through **#672**,
compared with checkout `a5de0b989ceb1046f4933b19cae7ef60bed0ede2`. The snapshot has 668 issue/PR records and
119 comments; all comment counts and input hashes were checked. No live network state or new experiment is implied.
Private fact databases, transcripts, personal session lists and local-only experiment artifacts are excluded.
When a report's input, runner or patch is unavailable, its result is an archived claim with that limitation;
a source link is not a downloadable copy of the private evidence.

## Decision sequence and superseded work

- The [#636 ordering decision](https://github.com/juev/memcue/issues/636#issuecomment-6679383966) put the system
  baseline first, with #637, #638, #643 and #645–#648 waiting. The [baseline](memory-baseline-636-2026-11.md)
  subsequently measured +0.23 on 40 memory prompts, with a 95% interval of [+0.07, +0.39]; the ten ordinary prompts
  were a separate stratum. The [card/search split](card-search-split-636-2026-11.md) and
  [search-policy comparison](search-policy-647-2026-11.md) followed. This archive records existing outcomes and
  does not execute queued work.
- [#656](https://github.com/juev/memcue/issues/656) removed the card and channel-labelling task. The proposed
  tracker filter (#637), consolidated profile (#645) and repository summary (#646) did not run as proposed and
  were superseded. Earlier card reports remain evidence for their own versions; plan-mode answer grading did not
  establish compliance with standing rules during real actions.
- [Dream cost](dream-cost-643-2026-11.md) preceded capture reduction (#638); [ablations](dream-ablations-661-2026-11.md)
  led to a daily interval (#665), not a demonstrated license to remove any task over arbitrary periods. The
  explicit cost budget remained a proposal. #648's source/freshness comparison was deferred, not completed.
- [#669](https://github.com/juev/memcue/issues/669) later chose English-first agent instructions and no new dream
  translation. Existing indexed translations remain usable. Its regression/CI evidence did not measure language
  compliance or translation-only recall loss; #661 removed expansion and translation together.
- Corrections matter: [#382/#383](pull-hermes-010-2026-10.md#repair-verified-after-release) separate an invalid
  missing-index claim from the real partial-number repair. [#474/#478](card-and-capture-observations-2026-10.md)
  separate whole-turn auditor probes, mixed-era adoption counts and the final 17-session pull baseline.
  [#511](card-and-capture-observations-2026-10.md#failed-or-inconclusive-synthetic-checks) left 64 answers without
  valid verdicts; those answers are not successful E2E evidence.

## Migrated issue and PR results

| Report |
|---|
| [Card delivery, capture and ordinary-work adoption, October 2026](card-and-capture-observations-2026-10.md) |
| [Early capture, labelling and vector measurements, October 2026](early-capture-2026-10.md) |
| [Field observations and corrected diagnoses, October 2026](field-observations-2026-10.md) |
| [Text-store audit outcomes, November 2026](store-audits-2026-11.md) |

## Memory, capture and end-to-end use

| Report |
|---|
| [A bench that holds the binary to what the docs say, October 2026](bench-2026-10.md) |
| [memcue against the old file memory, end to end, October 2026](e2e-files-2026-10.md) |
| [Facts about a file when the agent reads it: the count before a model call, November 2026 (#598)](file-context-598-2026-11.md) |
| [Facts that name a file, at the moment of a Read: the Mac stage, November 2026 (#598)](file-context-598-mac-2026-11.md) |
| [The Hermes plugin on a live installation: adoption, the price of an answer, and the thin-answer hint, October 2026](hermes-plugin-2026-10.md) |
| [Raw session history after an empty memcue search: a local first stage, November 2026 (#602)](history-fallback-602-2026-11.md) |
| [Raw session history after an empty memcue search: the Mac stage, November 2026 (#602)](history-fallback-602-mac-2026-11.md) |
| [Installation state is not a fact: the rule at extraction and in vet, October 2026](installation-state-426-2026-10.md) |
| [Memory against no memory, on real prompts, November 2026 (#636)](memory-baseline-636-2026-11.md) |
| [How other agent memories work, and what memcue should take, October 2026](other-memories-2026-10.md) |
| [A pitfall found by running something: S06 and one more line, October 2026 (#566)](pitfall-capture-566-2026-10.md) |
| [0.20.0 after the upgrade: the post-release check, October 2026](post-release-020-2026-10.md) |
| [An acknowledged preference: one more line in the session note, November 2026 (#601)](preference-capture-601-2026-11.md) |
| [A response preference is a rule: the line measured, November 2026 (#613)](preference-capture-613-2026-11.md) |
| [Pull instead of push, end to end, October 2026](pull-2026-10.md) |
| [Pull at 0.10.0 on a live installation: tests, adoption, and what the search hands over, October 2026](pull-hermes-010-2026-10.md) |
| [Pull at 0.14.0 on a live installation: what the floor does to the answer, October 2026](pull-hermes-014-2026-10.md) |
| [Pull at 0.16.0 on a live installation: the check repeated, October 2026](pull-hermes-016-2026-10.md) |
| [Pull in Hermes: what the live base gives, October 2026](pull-hermes-2026-10.md) |
| [Main after 0.16.0 on a live installation: the pre-release check, October 2026](pull-hermes-main-2026-10.md) |
| [Capturing research findings and finding them again, October 2026 (#531)](research-shelf-531-2026-10.md) |
| [The research shelf: a confirming run on an isolated stand, October 2026 (#531)](research-shelf-confirm-531-2026-10.md) |
| [When the agent searches, without a card, on real prompts, November 2026 (#647)](search-policy-647-2026-11.md) |
| [The source of an agent's fact, October 2026 (#574)](source-default-574-2026-10.md) |

## Cards and scope

| Report |
|---|
| [What the session card carries today, November 2026 (#606, first stage)](card-audit-606-2026-11.md) |
| [The global part of the card from selected facts, on real prompts, November 2026 (#623)](card-global-select-623-2026-11.md) |
| [The session card and the memory block: what the card can and cannot drop — October 2026](card-memory-copies-424-2026-10.md) |
| [The card says the scope and the date once per group, October 2026](card-metadata-437-2026-10.md) |
| [The Hermes card keeps the lines that repeat the memory notes — October 2026](card-no-plugin-filter-456-2026-10.md) |
| [Session-card classification and exploratory replay (#606)](card-replay-606-2026-11.md) |
| [The card against search, on real prompts, November 2026 (after #636)](card-search-split-636-2026-11.md) |
| [The session card trimmed to behaviour, on real prompts, November 2026 (#606)](card-trim-606-2026-11.md) |
| [Global facts that belong to one project, November 2026 (#607)](global-scope-607-2026-11.md) |
| [A memory note is searchable when it is written, October 2026](mirror-approved-440-2026-10.md) |
| [The scope of a search: a suffix key and the repository default, October 2026](scope-418-2026-10.md) |
| [The repository default against the whole base: keep `-in all` in Hermes, October 2026](scope-default-vs-all-438-2026-10.md) |
| [The place of a fact in recall, October 2026](scope-filter-2026-10.md) |

## Search and language

| Report |
|---|
| [Chinese queries: segment, translate, or index a third language, October 2026](chinese-2026-10.md) |
| [An empty answer that has facts below the floor hands over the first of them, October 2026](empty-answer-439-2026-10.md) |
| [One language for the base: duplicates, storage and search, October 2026](language-2026-10.md) |
| [The default limit against the model's habit: five stays — October 2026](limit-default-425-2026-10.md) |
| [The floor on a message: two changes, October 2026](message-floor-417-2026-10.md) |
| [What a message's noise is made of: two mechanisms tried and dropped, October 2026](message-noise-436-2026-10.md) |
| [Versions as exact terms: what a numeric anchor buys, October 2026](number-terms-2026-10.md) |
| [The lexical search chain: what each step does to the answer, October 2026](search-chain-2026-10.md) |
| [A floor for search: N as a maximum, October 2026](search-floor-2026-10.md) |
| [Lexical, E5 and hybrid retrieval on authored synthetic data, November 2026 (#682)](hybrid-682-synthetic-pilot.md) |
| [The search alone on labelled synthetic cases, October 2026 (#510)](search-synthetic-510-2026-10.md) |
| [A hint for a thin answer: what the model does when the floor hides facts, October 2026](thin-answer-hint-2026-10.md) |
| [The thin-answer hint counted the limit, not the tail — fixed, October 2026](thin-hint-tail-423-2026-10.md) |

## Readers, ranking and model choices

| Report |
|---|
| [A read channel keyed on tool calls: anchors, October 2026](anchors-2026-10.md) |
| [Gold set: which reader of the base earns its keep, October 2026](gold-set-2026-10.md) |
| [Jev as the retriever, October 2026](jev-judge-2026-10.md) |
| [A stable cut for the judge: a tie band that does not work, October 2026](judge-cut-2026-10.md) |
| [Skipping the judge on a strong lexical hit, October 2026](judge-gate-2026-10.md) |
| [The listwise reader on the product path, October 2026](listwise-2026-10.md) |
| [Local readers against the Jev judge, October 2026](local-readers-2026-10.md) |
| [Recall without a remote judge: local options, October 2026](local-recall-2026-10.md) |
| [GPT-6 Luna Decisions against Jev as the reranker, October 2026](luna-decisions-2026-10.md) |
| [The main model in the judge's roles, October 2026](main-judge-2026-10.md) |
| [A/B: the main chat model as the reranker](main-reranker-2026-10.md) |
| [How many facts one prompt should get: the MaxFacts cap, October 2026](max-facts-2026-10.md) |
| [One model or three: what recall and the background jobs need, October 2026](one-model-2026-10.md) |
| [Reasoning off per call, October 2026](reasoning-off-2026-10.md) |
| [Recall misses: where a useful fact is lost, October 2026](recall-misses-2026-10.md) |
| [The rerank threshold between 0.5 and 0.7, October 2026](threshold-2026-10.md) |
| [The duplicate judge over the neighbours, October 2026](vet-neighbours-2026-10.md) |

## Dream maintenance

| Report |
|---|
| [The dream without one task, against real searches, November 2026 (#661)](dream-ablations-661-2026-11.md) |
| [What the dream costs, by task, November 2026 (#643)](dream-cost-643-2026-11.md) |
| [The merge sweep: duplicates older than the last dream, October 2026](dream-merge-2026-10.md) |
| [The dream through the extraction endpoint, October 2026](dream-run-2026-10.md) |
| [Where the time of `dream -run -all` goes, October 2026](dream-time-2026-10.md) |
| [Dream triage research, October 2026](dream-triage-2026-10.md) |

## Storage, builds and operational checks

| Report |
|---|
| [Why the release binary is 15 MB, October 2026](binary-size-2026-10.md) |
| [Git history growth of the fact base, October 2026](git-growth-530-2026-10.md) |
| [What opening the base costs, October 2026](open-cost-2026-10.md) |
| [Releasing 0.22.0 and moving to the text history, October 2026](release-0-22-0-555-2026-10.md) |
| [SQLite or DuckDB for the fact base, October 2026](storage-2026-10.md) |
| [Alternatives to SQLite for the sync server: Turso and git-backed databases, November 2026 (#714)](storage-alternatives-714-2026-11.md) |
| [Compacting the text history: what the merge needs, October 2026 (#575, step 1)](text-history-compaction-575-2026-10.md) |
| [The cost of opening a text history, October 2026](text-open-cost-557-2026-10.md) |

## Reading and extending the archive

Use the coverage map to follow an issue to its report and source. Several studies reuse prompt pools, but change
bases, candidate sets, labels, versions or models; retain their separate denominators. Counts of searches, delivered
facts and card lines do not establish answer quality. Preserve UNKNOWN outcomes, unavailable inputs, failed attempts,
noise estimates and later corrections when adding a follow-up. Link the exact issue comment or PR containing the
result and state which proposed comparisons were never run.
