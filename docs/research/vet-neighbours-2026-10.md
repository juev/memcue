# The duplicate judge over the neighbours, October 2026

Measurements of 2026-10-22 for #240. The document gives aggregate numbers only: the bases hold host names, paths and
private repositories. The script is [scripts/vet_neighbours](scripts/vet_neighbours/main.go).

## Question

`vet` asked Jev about every candidate together with the whole approved base, in requests of 400 facts. On a base of
about 2,600 approved facts that is 7 requests and about 580k input tokens per candidate (`vet.log`, 2026-10-22). A
copy of a fact shares its words, so the facts BM25 puts first should hold every copy. Does asking about those only
retire the same candidates?

## Sample

The sample the threshold was set on ([jev-judge-2026-10.md](jev-judge-2026-10.md), experiment 6): the private base
of that research as it stood at 2026-10-20T23:52Z, rebuilt from the base's own versions, with 235 approved facts and
41 candidates. The labels of that research were not kept, so the candidates were labelled again by the review model
through `claude -p` (`claude-opus-5-5`, `memcue vet -dry-run -json` with the duplicate judge off, $0.58): 11 drop,
29 keep, 1 split. A drop is the positive class, as before; it is the review model's opinion, not the truth.

Neighbours: `recall.Nearest` over the candidate's text and keywords, the first N approved facts in BM25 order, past
the term and coverage gate. Every shape was run twice; `jev-1.14` through RouterAI, 328 calls, no errors.

## Results

| Shape | Run | AUC | ≥ 0.3: drop / keep | ≥ 0.5: drop / keep | ≥ 0.7: drop / keep | Input tokens per candidate |
|---|---|---|---|---|---|---|
| whole base, 235 | 1 | 0.803 | 8 / 3 | 5 / 1 | 2 / 0 | 45,636 |
| whole base, 235 | 2 | 0.797 | 8 / 3 | 5 / 1 | 2 / 0 | |
| 50 neighbours | 1 | 0.833 | 7 / 2 | 5 / 1 | 3 / 0 | 10,285 |
| 50 neighbours | 2 | 0.861 | 8 / 1 | 5 / 1 | 3 / 1 | |
| 30 neighbours | 1 | 0.861 | 9 / 2 | 5 / 1 | 2 / 0 | 6,369 |
| 30 neighbours | 2 | 0.870 | 9 / 2 | 4 / 1 | 2 / 0 | |
| 20 neighbours | 1 | 0.853 | 7 / 1 | 3 / 1 | 2 / 0 | 4,448 |
| 20 neighbours | 2 | 0.852 | 7 / 2 | 4 / 1 | 1 / 0 | |

- At the default threshold of 0.5, 50 neighbours retire as many drops and as many keeps as the whole base in both
  runs. 30 neighbours lose one drop in one run, 20 neighbours one or two.
- Every fact the whole base named at 0.5 or above was among the first 20 neighbours. The search finds the copy; where
  a smaller shape loses a drop, the same pair scored below 0.5 in that request. Whether that is the shorter list or
  the jitter between runs is not separated.
- AUC is higher with neighbours in every run (0.833–0.870 against 0.797–0.803); why is not measured.

On the author's base, 2,670 approved facts and the 6 candidates in its queue, 50 neighbours cost 12,331 input tokens
per candidate, against about 580k for the whole base in 7 requests.

## Decision

`vet` asks about the 50 nearest approved facts in one request (`MEMCUE_JUDGE_DUP_MAX_FACTS`, default 50). A
candidate that shares no words with the base is not asked about: it has no neighbour to repeat.

## Mistakes in the method

- The labels are a fresh run of the review model, not the 15/26 of the threshold research, and a different model may
  have given those. Both shapes are compared on the same labels, so the comparison holds; the absolute numbers differ
  from the README's earlier ones.
- 41 candidates and two runs. A difference of one candidate is within the jitter of the judge, which moves the same
  pair by 0.1–0.2 between runs.
- The token count on the big base is for 50 neighbours only; the whole-base figure there comes from `vet.log`, not
  from this run.

## Reproduce

```bash
MEMCUE_JUDGE_URL=… MEMCUE_JUDGE_MODEL=… MEMCUE_JUDGE_KEY=… \
    go run -tags research ./docs/research/scripts/vet_neighbours -facts FACTS.db -n 20,30,50 -runs 2 > out.jsonl
env -u MEMCUE_JUDGE_URL -u MEMCUE_API_URL -u MEMCUE_MODEL MEMCUE_FACTS=COPY.db memcue vet -dry-run -json > labels.json
```
