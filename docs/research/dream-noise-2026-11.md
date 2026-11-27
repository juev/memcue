# Dream noise classification on 60 synthetic facts

Measured on 2026-11-27. None of the tested classifiers met the control criterion on both batch layouts. Flash passed on the first layout. Repeating the control with the same records in different batches yielded two false candidates and one missed noise record.

## What limits the current filter

The [fixture](scripts/dream_noise/fixture.json) contains 60 invented English and Russian records. Each of its two partitions has 12 noise, 12 useful, and 6 uncertain records. The partitions include incidental updates, mixed records with useful findings, and protected metadata. The runner sends only local IDs and text to the classifiers. It uses the existing `CaptureNoise` for the three filter comparisons.

| Control filter | Noise admitted | Noise excluded |
| --- | ---: | --- |
| Source, kind, and scope | 9/12 | f33, f35, f36 |
| Text pattern | 6/12 | f32, f34, f35, f36, f38, f41 |
| Both, as in dream | 5/12 | f32, f33, f34, f35, f36, f38, f41 |

Removing the metadata check alone would add one noise record, f33. The other two protected noise records also lack a matching phrase. The combined filter admitted one uncertain record, f55. Admission measures what reaches review. The model still decides whether to propose retirement. The fixture measures these mechanisms on chosen cases, so the counts cannot estimate how much production noise each check excludes.

## Classifier results

Each Jev request held up to 15 records and asked three questions per record: two independent `noul` questions about an episode and reusable knowledge, and one `choice` with noise, useful, and uncertain outcomes. The model returned `typesafe/jev-1.14-20261014` via [RouterAI](https://routerai.ru/models/typesafe/jev-1.14). Flash used `deepseek-flash` with thinking disabled and the same three outcome definitions. Jev thresholds were selected using calibration labels alone. For the second control layout, every model received the same records rearranged across the two batches.

| Classifier | Calibration noise found | Control noise found | Control false candidates | Changed nominations after regrouping |
| --- | ---: | ---: | --- | --- |
| Jev `noul`, one-off ≥ 0.80 and reusable ≤ 0.30 | 5/12 | 6/12 | 0 | 0 |
| Jev `choice`, noise ≥ 0.70 | 10/12 | 10/12 | 1 (f55) | 2 (f31, f57) |
| Flash, three labels | 12/12 | 12/12 | 0 | 3 (f32, f55, f57) |

Jev's `noul` missed f31, f32, f36, f37, f38, and f40. For f31, a disposable token expired after a preview. Jev scored it 0.98 for a one-off episode and 0.68 for reusable knowledge, so the second threshold prevented nomination. The direct `choice` missed f32 and f36 and nominated f55, whose log lacks the error line. After regrouping, Flash classified f32 as uncertain and nominated f55 and f57, both labeled uncertain. This gives Flash 11/12 noise found and two false candidates in the second layout. The experiment supports keeping these scores as review evidence only.

## What splitting cost in the replay

Memcue 0.40.7 prepared one bounded packet from the same fixture. It contained 40 inputs for splitting and 11 groups. Two independent copies of the base received the full answer and the answer generated with the split inputs omitted. Group and scope inputs stayed identical.

| Arm | Input tokens | Output tokens | Estimated peak cost | Call time | Accepted group actions | Split parts | Recall controls on one hit |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Full packet | 5,205 | 25,096 | $0.03115 | 174 s | 5 | 6 from 3 splits | 5/6 |
| Same packet without split inputs | 3,458 | 5,263 | $0.00679 | 23 s | 3 | 0 | 6/6 |

Both arms retired f37, f39 and f40. The full arm also corrected f03 and retired f27 as its duplicate. The smaller packet met the cost target based on the peak-price estimates in the table. It missed two accepted group actions, so this replay gives insufficient evidence to change splitting. Native acceptance establishes that an action passed apply validation. The model's judgment about the facts remains unverified.

Before the replay, all six search controls had a matching fact at rank one. The full arm put the exporter's UTF-8 encoding and LF line endings in separate facts, which appeared at ranks two and one respectively. The control requires both anchors in the same hit. The other five controls kept their ranks in both arms. A single paired replay gives limited evidence about response variance and about the effect on a long-running base.

## Spend

The run made 14 paid requests. Six Jev calls used 36,960 input tokens, or about ₽0.17 at the RouterAI listed price. Six Flash classification calls used 3,255 input and 691 output tokens, or about $0.00181 at peak rates. The two replay calls cost an estimated $0.03794 at peak rates. The combined estimate was **under $0.044** using the runner's conservative allowance for RouterAI. These estimates use returned usage and list prices. The ledger uses peak DeepSeek rates and expresses Jev's comparison cost at the direct TypeSafe rate. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/) · [RouterAI pricing](https://routerai.ru/models/typesafe/jev-1.14) · [TypeSafe pricing](https://docs.typesafe.ai/models)

## Follow-up on 24 fresh synthetic records

The follow-up used [24 fresh records](scripts/dream_noise/followup_fixture.json), split into two fixed batches of 12. The set contained 10 noise, 8 useful and 6 uncertain records. Jev received one question per record and one question type per request. The standalone `reusable` question kept its original wording. The new Choice rubric gives priority to concrete reusable knowledge, then to an unresolved diagnostic lead with missing evidence, then to routine or completed status. Thresholds were selected using only the first experiment's 30 calibration records.

DeepSeek received the same Choice rubric and the same batches in `none`, `low` and `high`. A second `none` call for each batch used the identical request payload.

| Variant | Noise found | False candidates | Output tokens for the control | Estimated peak control cost |
| --- | ---: | ---: | ---: | ---: |
| Jev `reusable` ≤ 0.20 | 5/10 | 0 | 512 | $0.00017 equivalent |
| Jev `choice`, noise ≥ 0.70 | 9/10 | 0 | 1,012 | $0.00027 equivalent |
| DeepSeek `none` | 10/10 | 0 | 206 | $0.00057 |
| DeepSeek `none`, identical repeat | 10/10 | 0 | 169 | $0.00030 |
| DeepSeek `low` | 10/10 | 0 | 1,079 | $0.00163 |
| DeepSeek `high` | 10/10 | 0 | 1,708 | $0.00239 |

Jev Choice missed h11, a passed smoke test with no details about its earlier failure. It chose `uncertain` at 0.58. The two DeepSeek `none` runs returned the same 24 labels. They matched 23 reference labels. Both thinking modes labeled h21 as useful. That record describes the protocol-compatibility condition under which a regression occurs. The set of noise candidates was identical across all four DeepSeek runs.

The test supports `none` for low-cost candidate review on these examples. It gives no evidence that paying for thinking improves noise selection here. The previous experiment's inconsistent outcomes came from different records and a change of batch layout. This follow-up tests repeatability with a fixed input on the fresh set. The two Jev Choice prompts also used different control sets, so their measured difference includes both wording and sample effects.

The follow-up used 16 paid requests. The conservative spend estimate from the request ledger was $0.00714. The Jev costs in the table use the direct TypeSafe rate for comparison. The second `none` run had 768 cached input tokens. [DeepSeek thinking settings](https://api-docs.deepseek.com/guides/thinking_mode/)

## Jev noise and pair decisions with a generative fallback

The [pipeline fixture](scripts/dream_noise/pipeline_fixture.json) contains 20 invented facts with eight expected actions. Memcue 0.40.7 prepared nine groups from those facts. The native control gave all nine groups to DeepSeek with thinking enabled. Both arms omitted split inputs. The fixture has no scope changes.

The Jev arm ran the follow-up Choice prompt first. It removed noise candidates from the simulated input to subsequent checks at the fixed threshold of 0.70. For each remaining same-project group it sent one stable anchor in `state` and the neighbors in structured question instructions. Each `noul` answered one question about coverage, an explicit replacement, a resolved workaround, a conflict, or a complementary detail. The retirement threshold was fixed at 0.90. The runner forwarded the one group marked for a text merge to native dream, restricting both the prompt and the packet to that group's members. Native answers were applied to disposable fixture copies. Jev decisions were scored as proposals.

| Arm | Exact expected actions | Expected effects | Unexpected effects | Groups sent to DeepSeek |
| --- | ---: | ---: | ---: | ---: |
| Native group review | 6/8 | 7/8 | 0 | 9 |
| Jev decisions alone | 5/8 | 5/8 | 0 | 0 |
| Jev with one native fallback | 7/8 | 7/8 | 0 | 1 |

The native arm called the resolved workaround outdated. Both decisions retired the same record with the same evidence ID, so they count as the same effect. The native arm missed f17, a temporary UI record outside the regex allowlist. Jev screened it before comparing its group and kept the useful f20. Jev also screened the regex-eligible f16. It missed f04 because the explicit replacement scored 0.86 against the 0.90 threshold. An unresolved conflict scored 0.95 for incompatibility and only 0.15 for replacement. The runner kept both facts. The one fallback corrected the backup rule and retired the duplicate while preserving both commands and the rotation interval.

| Arm | Successful API calls | DeepSeek output tokens | Estimated off-peak cost without cache |
| --- | ---: | ---: | ---: |
| Native group review | 1 | 1,809 | $0.00160 |
| Jev with native fallback | 9 | 559 | $0.00129 |

The estimate uses observed usage, DeepSeek's published off-peak rates without cache, and RouterAI's ₽4.48 per million input tokens converted at the November 27 Bank of Russia rate. That gives about 19% savings off-peak or 39% at peak on this fixture. Both DeepSeek calls actually reported 1,792 cached input tokens. At off-peak rates with that cache usage, the estimates are $0.00134 and $0.00103. The Jev arm traded one full reasoning packet for eight decisions requests and one smaller reasoning packet. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/) · [RouterAI pricing](https://routerai.ru/models/typesafe/jev-1.14) · [Bank of Russia rate](https://www.cbr.ru/currency_base/daily/?UniDbQuery.Posted=True&UniDbQuery.To=27.11.2026)

The first run sent record IDs as credentials for six pair requests and got HTTP 401. After fixing the runner, it reused the successful noise responses and the native control and repeated only the rejected pair requests. The full ledger contains 16 attempts, 10 successful replies with token usage, and six authentication errors. Estimated successful usage cost about $0.0024 at the off-peak rate with the observed cache.

The extra noise candidate and the missed replacement suggest two places to focus the next replay. Test a route that sends uncertain replacements to the reasoner, and measure the added cost when more groups reach that route. Reuse the same pair questions with non-thinking Flash to measure the gain from Jev compared with a single-provider pipeline. This run evaluates one small authored fixture and one execution per arm.

## The same route with DeepSeek and thinking disabled

The single-provider arm reused the 20 facts, the nine native groups, the expected actions, and the earlier native control. DeepSeek Flash with thinking disabled used the same noise rubric and the same five questions for each pair. It returned yes, no, or uncertain for the pair questions. The adapter mapped those labels to 1, 0, and 0.5 for the shared routing code. These values encode decisions. Jev returns model scores with thresholds. The test compares the two end-to-end policies on one fixture.

| Arm | Expected effects | Unexpected effects | Groups sent to reasoning | Successful requests per arm | Estimated off-peak cost without cache |
| --- | ---: | ---: | ---: | ---: | ---: |
| Native group review | 7/8 | 0 | 9 | 1 | $0.00160 |
| Jev with native fallback | 7/8 | 0 | 1 | 9 | $0.00129 |
| Flash without thinking, with native fallback | 8/8 | 0 | 1 | 9 | $0.00200 |

Flash proposed both noise retirements, both complete duplicates, the explicit TSV-to-CSV replacement, and the resolved workaround. It kept the contradictory pair with no replacement evidence and the two reports about different hosts. The same backup-key group went to the reasoner, which preserved the interval and commands and produced the two remaining expected actions. Flash recovered the explicit replacement that Jev scored 0.86 and kept below its retirement threshold.

Eight non-thinking Flash calls read 5,986 input tokens and emitted 458 output tokens. Their estimated cold off-peak cost was $0.00117. The fallback added 2,274 input and 804 output tokens. The Jev fallback for the same two facts emitted 559 output tokens. Even when pricing both routes with that smaller fallback, Flash would cost $0.00185, above the native control's $0.00160 on this fixture. With observed cache hits, the estimates for the native, Jev, and Flash routes were $0.00134, $0.00103, and $0.00173. The estimates use the same reference rates as the previous section. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/)

The Flash route made nine new requests. The native baseline was reused. This was one run on an authored fixture. A useful next comparison would pack multiple anchor groups into one Flash request so the pair instructions are paid once, then check whether the same actions survive regrouping. This run shows where batching would have to save cost.

## Packing anchor groups into each Flash request

These two arms reused the 20-record fixture, the noise decisions, and the accepted two actions for the same reasoning fallback. Only the pair stage made new calls. It had six anchor groups and seven neighbor pairs. The grouped request included the five existing criteria once, followed by each anchor and its neighbors. Flash used thinking disabled and returned one label per pair criterion. Each grouped arm ran with forward and reversed group order.

| Pair layout | Pair requests | Pair input / output tokens | Expected effects | Unexpected effects | Full route, off-peak without cache |
| --- | ---: | ---: | ---: | ---: | ---: |
| One group per request, earlier control | 6 | 5,005 / 298 | 8/8 | 0 | $0.00200 |
| Six groups in one request, forward | 1 | 1,027 / 246 | 8/8 | 0 | $0.00137 |
| Six groups in one request, reverse | 1 | 1,027 / 258 | 8/8 | 1 | $0.00138 |
| Two requests of three groups, forward | 2 | 1,488 / 247 | 8/8 | 0 | $0.00144 |
| Two requests of three groups, reverse | 2 | 1,488 / 251 | 8/8 | 1 | $0.00144 |

The full-route estimates add the earlier two noise calls and one reasoning fallback to the measured pair calls at the same cold off-peak prices. The forward six-group run cut estimated route cost by 31%. Both grouped layouts also cost less than the $0.00160 native control on this fixture. The six new calls cost about $0.00107 at off-peak rates with the returned cache counts. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/)

The reverse six-group run proposed retiring f11 as a duplicate of f10. The facts describe the same service on distinct hosts. The reverse three-group run proposed retiring f12 as outdated by f13. Neither record establishes that one batch-size setting replaced the other. In both cases the corresponding forward run kept the records. The second requests changed group order and sampled new replies, so this test leaves the cause of the differences unresolved.

Neither grouped layout passed both safety checks on this fixture. The cost reduction supports testing grouped judgments as nominations, followed by an independent check before retirement. A comparison that uses explicit evidence for entity identity and replacement would show how much of the savings survives confirmation.

## Confirming proposed pair retirements

This follow-up replayed the four saved grouped runs over the same 20 synthetic facts. For each proposed pair retirement, a new Flash request with thinking disabled received only the candidate record, the evidence record, and the criterion for that retirement. Duplicate checks require the same entity and environment and preservation of all reusable claims. Replacement and resolution checks require explicit evidence that applies to the same system. The replay applies only pair retirements that receive `confirm`. Noise decisions and the two actions from the earlier reasoning fallback were reused.

The four runs proposed six distinct pair retirements. Flash confirmed all four expected pair retirements (f02, f03, f04, and f06). It returned `reject` for f11 by f10, which described separate hosts, and `uncertain` for f12 by f13, where the text supplied two values without evidence that one replaced the other. Each full replay matched all eight expected actions, including their exact kinds.

| Pair layout | Confirmation calls | Full route calls | Expected effects | Unexpected effects before / after | Full route, off-peak without cache | Savings vs. native |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Native control, earlier run | 0 | 1 | 7/8 | 0 / 0 | $0.00160485 | — |
| Six groups in one request, forward | 4 | 8 | 8/8 | 0 / 0 | $0.00148980 | 7.2% |
| Six groups in one request, reverse | 5 | 9 | 8/8 | 1 / 0 | $0.00152790 | 4.8% |
| Two requests of three groups, forward | 4 | 9 | 8/8 | 0 / 0 | $0.00155955 | 2.8% |
| Two requests of three groups, reverse | 5 | 10 | 8/8 | 1 / 0 | $0.00159015 | 0.9% |

The six new calls returned valid verdicts without retries, using 1,028 input and 44 output tokens with zero cached tokens. Their estimated off-peak cost was $0.00018060. To estimate each route on its own, the table adds the two noise calls and the reasoning fallback from the earlier Flash run ($0.00106665 off-peak without cache), the measured grouped pair stage, and the confirmations that route would have requested, including rejected and uncertain proposals. The four common confirmations were reused across the experiment and charged to each route estimate. Savings are the difference from the native control divided by the native control cost. The estimates use $0.15 per million input tokens and $0.60 per million output tokens. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/)

Six groups per request gave the lowest cost across both orders. The criteria were written after inspecting the two false retirements, and each distinct proposal received one verdict. A separate small synthetic fixture with held-out cases is needed to measure how often confirmation blocks a valid retirement or lets a new error through.

## Six-group route on a separate fixture

[The holdout fixture](scripts/dream_noise/pipeline_holdout.json) contains 16 new synthetic facts and seven expected actions. The fixture and expected actions were fixed before any model calls. Memcue 0.40.7 produced six native two-fact groups in an isolated SQLite database. The experiment reused the earlier prompts and routing rules for noise, packed pairs, and confirmation. It shared the two noise replies between group orders, reused the fallback reply when both orders escalated the same groups, and confirmed each distinct direct pair retirement once.

| Route | Expected actions before / after confirmation | Unexpected actions before / after | Calls per route | Off-peak without cache | Off-peak with observed cache |
| --- | ---: | ---: | ---: | ---: | ---: |
| Native control | 6/7 | 0 | 1 | $0.00187275 | $0.00160933 |
| Six groups in one request, forward | 7/7 / 7/7 | 0 / 0 | 7 | $0.00160140 | $0.00128153 |
| Six groups in one request, reverse | 7/7 / 7/7 | 0 / 0 | 7 | $0.00154680 | $0.00122693 |

The native control applied all six expected pair and correction actions. Its preparation omitted the one-off record h13 from the groups and noise candidates, as verified by a local repeat of `dream -prepare -all`. Flash screened all 16 facts and classified h13 as noise, h14 as useful despite its one-off framing, and h16 as uncertain. Both group orders proposed the same three direct retirements, which the confirmation calls accepted. Both kept the workaround after a staging-only rollout. They sent the complementary procedure and the more detailed snapshot procedure to the reasoning fallback. The fallback preserved the renewed key identifier in h09 and retired h10 and h12.

Two additional checks sent known-wrong proposals directly to the same confirmation prompt in separate requests. Flash returned `reject` for retiring h07 as resolved by h08 because production still needs the workaround. It also returned `reject` for retiring the detailed h11 as a duplicate of h12 because the shorter record would lose command and digest verification details. These probes did not change the route outcomes or route cost estimates. They used 365 input and 16 output tokens.

Each route estimate includes its noise calls, one packed pair call, three confirmations, and one reasoning fallback at the rates above. That gives 14.5% lower cost in forward order and 17.4% in reverse order relative to the native control without cache. Both orders produced the same routing decisions and actions. The `conflict` labels for h03/h04 and h05/h06 changed from `yes` to `no`. Explicit replacement and resolution take priority over the conflict label in the router. The cost difference came from 91 more output tokens in the forward packed reply. The experiment made 11 successful calls with no retries, including the two probes. Its estimated cost was $0.00319 off-peak with observed cache usage. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/)

Screening all facts caught a noise record that the native eligibility filter omitted. The packed stage made no false nominations on this fixture. The two injected probes test the confirmation prompt in isolation. The runs share noise and fallback replies, so the order comparison covers the packed pair stage. The experiment measures the action stream with splitting excluded.

## Applying the saved holdout decisions

Both group orders produced the same seven actions after confirmation and fallback. The offline replay remapped their fixture IDs to the IDs from a fresh SQLite import and submitted the actions to memcue 0.40.7 using `dream -apply`. Each arm started from a separate copy of the database and state created by `dream -prepare -all`. The replay made zero model calls.

| Packet and database | Actions applied | Refusal | Active facts after apply |
| --- | ---: | --- | ---: |
| Native packet | 6/7 | h13 was outside the packet groups | 11 |
| Packet with h13 as a singleton and an eligible noise candidate | 7/7 | None | 10 |
| Same eligibility, with h13 corrected after preparation | 6/7 | h13 had changed since the packet | 11 |

The experimental packet adds h13 to `groups` and `noise` at the version captured by preparation. The answer includes the packet ID and a reason for noise retirement. With that packet, the native command retired the six expected facts and wrote the h09 correction as a new active version with the renewed key identifier. The replay also checked the latest version, status, content, scope, kind, and source of all 16 facts against the expected SQLite state. The stale-version arm preserved the updated content of h13 and applied the other six actions.

The required integration point is packet preparation. On this fixture, native apply accepted the saved Flash decisions once the experimental packet carried the new noise candidate and its snapshot version. The version check still rejected a later edit.

## Running the full CLI

The experimental CLI route uses the same configured `deepseek-flash` endpoint for all stages. It screens up to 15 facts per call from the current bounded batch and sweep, including singletons. It compares up to six same-repository groups per call, then confirms each proposed pair retirement on its own. The Go process maps short request IDs back to the saved fact IDs and versions. The reasoning call handles splits, scope checks, and groups that need a merge or fall outside the same-repository comparison. The results go through the native apply checks.

The comparison ran one due pass with `-j 1` per arm on fresh copies of the 20-fact fixture and the 16-fact holdout. Both fixtures have repository scopes. The comparison used a local proxy to cap each fixture at 16 requests and $0.05 estimated at peak rates. The budgets covered both commands and included a completion cap for the reasoning call. The checks for valid actions use the existing action labels. The split counts are separate because the fixtures were authored before testing splits.

| Fixture | Arm | Expected action effects | Unexpected actions | Applied splits | Requests | Input / cached / output tokens | Elapsed time | Off-peak without cache |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Original, 20 facts | Native | 7/8 | 0 | 2 | 1 | 4,480 / 1,792 / 3,768 | 13.84 s | $0.00293280 |
| Original, 20 facts | Flash | 8/8 | 0 | 2 | 9 | 6,090 / 2,304 / 2,122 | 15.15 s | $0.00218670 |
| Holdout, 16 facts | Native | 6/7 | 0 | 1 | 1 | 3,901 / 1,792 / 4,218 | 18.97 s | $0.00311595 |
| Holdout, 16 facts | Flash | 7/7 | 0 | 2 | 7 | 5,515 / 2,176 / 4,472 | 23.76 s | $0.00351045 |

Flash retired the expected noise records f16, f17, and h13. Native retired f16. Both Flash runs preserved the expected pair and merge results, including the key ID in the h09 correction. The native holdout used `outdated` for one `resolved` retirement. Both labels produced the same database change.

The original fixture split the cedar and maple relay facts by host and setting in both arms. The Flash holdout split the staging-only change into a generic fix description and a separate rollout fact. The standalone fix description lost the staging qualifier. The native apply checks also refused a one-part split in that run. SQLite read-back confirmed the retained production workaround and the staging qualifier in the rollout record. Split quality remains a separate rollout criterion.

At the reference rates above, Flash cost 25.4% less on the original fixture and 12.7% more on the holdout without cache. Each Flash route spent most of its output tokens on the reasoning call. The sample consists of one due pass per measured arm and leaves cost variability across repeated runs open. [DeepSeek pricing](https://api-docs.deepseek.com/quick_start/pricing/)

An initial CLI run sent full fact IDs in the packed request. On the original fixture the response did not supply a valid label for one requested ID, the pass failed after three paid replies, and SQLite state stayed the same. On the holdout the initial pass applied the seven scored actions and one split. Its reasoning call produced 9,850 output tokens, bringing the cold off-peak estimate to $0.00744015. The rerun used short IDs and reused the two saved native controls. Across both runs, all 29 HTTP replies reported usage. Total estimated spend with the observed cache was $0.01879 off-peak.

The local endpoint tests exercise the real CLI flow with synthetic replies. They verify that a failed confirmation HTTP request leaves the facts and last-dream marker as they were, then confirm that a retry succeeds. They also verify that a concurrent edit blocks a stale noise retirement and that the reasoning requests receive split and scope tasks.

The targeted CLI and transport tests passed with the race detector. One full Go run hit a SQLite open error in `TestContentOnlyConcurrentLegacyTextOpens`. Running that test 100 times on a clean copy of the base commit reproduced the error three times. The next full Go run passed. The enclosing `make test` stopped in Hermes discovery because the local Python interpreter lacked `agent` and `hermes_cli`.

## Making Flash the default for DeepSeek dream

[#810](https://github.com/juev/memcue/issues/810) adopts the measured Flash route for ordinary dream runs when the configured model is `deepseek-flash` and an endpoint is set. Local manual and background runs and the server scheduler call the same pass. The server screens its fresh batch for noise and uses the pair stage for the weekly sweep. Old server noise candidates still require manual review, and the standalone `-noise` command keeps its own progress from #808. The existing apply checks use the versions from preparation.

Verification used isolated SQLite bases and local HTTP replies. A built binary ran plain `dream -run -j 1`
with two holdout facts. With `deepseek-flash` it sent noise and review requests, retired h13, and kept h14.
With another model it sent only the native review request and kept both facts. Go tests also covered the default
background path, HTTP failure and retry, stale decisions, fresh singleton noise on the server, and the weekly
pair sweep without old singleton screening. The full Go suite with the race detector, lint, and all 24 offline
research tests passed. These checks used zero paid requests.

## Response format during rollout

Two bounded runs on v0.40.8 stopped before apply. DeepSeek returned a JSON array under the `useful` label
instead of a label string under each requested fact ID. Both runs left the dream checkpoint and the fact counts
as they were. In v0.40.9 the noise prompt shows the preferred per-ID format, and the parser also accepts the
grouped format when the arrays assign one allowed label to every requested ID. Unknown, repeated, and missing
IDs fail the pass. Pair judgments and confirmations keep their existing response format.

## Reproduce

Run from the repository root with Python 3, Go, a local memcue binary, and API credentials supplied in the environment. The output directory must be new. The runner reserves peak cost before each call, caps completion tokens and records usage or failure in `requests.json`. `report.json` contains the two layouts, fact IDs, labels, scores, and replay results. The native replay imports the fixture into temporary SQLite files and runs with an isolated environment.

```sh
python3 docs/research/scripts/dream_noise/experiment.py \
  --binary "$(command -v memcue)" --out /path/to/new-output-directory

python3 docs/research/scripts/dream_noise/followup.py \
  --out /path/to/new-followup-directory

python3 docs/research/scripts/dream_noise/pipeline.py \
  --binary "$(command -v memcue)" --out /path/to/new-pipeline-directory

python3 docs/research/scripts/dream_noise/pipeline.py \
  --judge flash --binary "$(command -v memcue)" --out /path/to/new-flash-pipeline-directory \
  --reuse-baseline /path/to/new-pipeline-directory/report.json

python3 docs/research/scripts/dream_noise/flash_packed.py \
  --binary "$(command -v memcue)" --prior /path/to/new-flash-pipeline-directory/report.json \
  --out /path/to/new-packed-directory

python3 docs/research/scripts/dream_noise/flash_packed.py \
  --binary "$(command -v memcue)" --prior /path/to/new-flash-pipeline-directory/report.json \
  --groups-per-request 3 --out /path/to/new-packed-three-directory

python3 docs/research/scripts/dream_noise/flash_confirm.py \
  --packed /path/to/new-packed-directory/report.json \
  --packed /path/to/new-packed-three-directory/report.json \
  --out /path/to/new-confirm-directory

python3 docs/research/scripts/dream_noise/flash_holdout.py \
  --binary "$(command -v memcue)" --out /path/to/new-holdout-directory

python3 docs/research/scripts/dream_noise/apply_holdout.py \
  --binary "$(command -v memcue)" --results /path/to/new-holdout-directory \
  --out /path/to/new-apply-directory

python3 docs/research/scripts/dream_noise/cli_compare.py \
  --binary /path/to/flash/memcue --native-binary /path/to/pre-flash/memcue \
  --out /path/to/new-cli-directory

python3 -I docs/research/scripts/dream_noise/test_experiment.py
```

Set `DEEPSEEK_API_KEY` and `ROUTERAI_API_KEY` for the provider experiments, or pass `--router-key-file` for RouterAI. The apply replay reads the saved holdout results and runs locally without API keys.
Use `cli_compare.py --reuse-native DIRECTORY` in place of `--native-binary` to reuse a saved control for the same fixtures.
