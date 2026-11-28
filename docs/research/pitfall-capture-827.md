# Capture a verified pitfall before replying (#827)

The [first experiment](https://github.com/juev/memcue/issues/827#issuecomment-7237418768) found that giving DeepSeek a real tool result was not enough to make it save a reusable pitfall. Appending a paragraph yielded two usable writes of four, but raised search calls from 34 to 47. The initial baseline wrote one fact with an already-past expiry. This follow-up replaces the ambiguous requirement for a verified “cause and fix” with a short ordered decision: check the source, check reuse, check for a duplicate and commit the new fact before replying. It applies to [Hermes](../../plugins/hermes/memcue/__init__.py) only. Other harnesses and the binary are unchanged.

## Method

We used the direct DeepSeek API (`deepseek-flash` and `deepseek-v4-pro`, thinking disabled), the Hermes provider's genuine system block and tool schemas, and a clean build of [`7379259`](https://github.com/juev/memcue/commit/73792592f1deaa8e48ca9d5eddd029e256820b00) (v0.41.0). Both arms received the same trusted UTC date (2026-11-28), the same synthetic user tasks and tool outputs, up to seven requests per conversation and 1,536 output tokens per request, with no HTTP retries. An allowlisted fixture tool actually executed each failing and passing command on two disposable copies; the positive changed only the workaround input. Tool calls from the model passed through the native provider and local binary. Each conversation had a fresh synthetic Git repository, configuration and SQLite database. No live facts, credentials, provider reasoning or raw HTTP replies were published.

A pass requires exactly one committed new, ordinarily searchable `pitfall` in the actual repository scope with `source=tool`, the observed failure condition, a usable workaround and any relevant version. A tool call or a statement that memory was saved does not count by itself. Control cases require no mutation at all. Incomplete/model tool protocol failures are not counted as successful decisions. Before the paired validation we set a threshold of 10/12 usable positives, improvement over baseline, zero bad writes on controls, no more total search calls than the baseline and fresh model recall for every mechanism. The provider's rendered text and complete tool schemas were compared with the measured candidate byte for byte (the test harness supplies its own date line).

## Iterations before validation

| Prompt and model | Usable direct positives | Bad control decisions | Searches | Main failure |
|---|---:|---:|---:|---|
| Baseline, Flash, first 24-case grid | 2/12 | 0/12 | 68 | Repeatedly described the working workaround without storing it |
| Clarify condition and workable remedy, Flash, same grid | 3/12 | 0/12 | 46 | Wording alone still left capture optional in practice |
| Explicit before-reply action, Flash, same grid | 12/12 | 1/12 | 52 | A user's summary was mislabelled as tool evidence |
| Require direct results, Flash, second grid | 9/12 | 2/14 | 51 | Wrong repository scope in one write; treated summaries as direct evidence |
| Explicit message role and literal `repo`, Flash, second grid | 9/12 | 1/14 | 41 | Three incomplete positive tool frames and one duplicate |
| Explicit message role and literal `repo`, Pro, second grid | 12/12 | 1/14 | 23 | Stored one user-only summary as a tool finding |
| Ordered source/reuse/duplicate check, Flash, second grid | 12/12 | 0/14 | 34 | Passed on this development grid |
| Ordered source/reuse/duplicate check, Pro, second grid | 12/12 | 1/14 | 22 | Rewrote one existing equivalent fact |

The final wording checks provenance **before** the model treats a symptom as eligible and tells it exactly when to stop on a matching existing fact. It is an instruction, not an authorization or validation check in the storage layer. The Pro duplicate rewrite on the development grid remains evidence of model variance, not a resolved impossibility.

## Unseen validation fixtures

| Fixture | Actual observed trigger | Working change | Flash baseline | Flash new | Pro new |
|---|---|---|---:|---:|---:|
| Kestrel 7.2.1 | `KESTREL-71` with no tariff selector/default | `KESTREL_TARIFF=rules.csv` | 1/3 | 3/3 | 3/3 |
| Osprey current tree | `OSPREY-49` on comma-decimal input with dot default | `--decimal=comma` | 0/3 | 3/3 | 3/3 |
| Shrike 2.1.0 | `SHRIKE-26` because the default settings file is absent | `--config=config/probe.ini` | 0/3 | 3/3 | 3/3 |
| Snipe current tree | `SNIPE-45` with unqualified timestamps and no default zone | `--timezone=UTC` | 1/3 | 3/3 | 3/3 |

The 14 controls per arm were two runs each of a one-off, unverified cause/workaround, documented workaround, same-scope duplicate, unknown deployment/version material to the claim, byte-identical positive evidence relayed as **user text**, and an explicit do-not-write-memory request following direct evidence. No control mutated storage in any validation arm. The Flash baseline attempted and stored three positive facts; one was excluded because it used default `source=agent` despite direct evidence. The twelve new writes per model all used default expiry through 2026-12-28. Separate fresh conversations with each model found and used all four workarounds without additional writes.

| Decision metric | Flash baseline | Flash new | Pro new |
|---|---:|---:|---:|
| Completed conversations | 26/26 | 26/26 | 26/26 |
| Useful persisted positives | 2/12 | 12/12 | 12/12 |
| Control mutations | 0/14 | 0/14 | 0/14 |
| Search calls | 43 | 39 | 20 |
| API responses | 83 | 92 | 81 |
| Prompt tokens | 246,105 | 310,523 | 269,281 |
| Completion tokens | 11,851 | 12,998 | 10,310 |

The new wording improved capture without more searches on the paired Flash grid, but **did** increase total prompt tokens and API responses; committing more facts requires real round trips. Pro is a replication of the candidate on this holdout, not a paired Pro baseline. These are small synthetic fixtures with repeated mechanisms, not independent population samples. Neither deployment nor end-to-end testing of a live Hermes gateway was part of the inference run. Nothing here justifies hard-coding source enforcement in the storage layer or generalizing to other harnesses without testing them.
