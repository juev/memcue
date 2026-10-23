# Local readers against the Jev judge, October 2026

Status: **measured 2026-10-23 (#316, #317, #320); no local reader replaces Jev.** The question was whether recall can
order and cut the words' candidates without a remote call ([local-recall](local-recall-2026-10.md), sections 2.2, 2.3
and steps 2–4 of section 5). Three local readers were scored on the gold set against Jev: gradient-boosted trees over
the words' own signals, a published multilingual cross-encoder, and the same cross-encoder fine-tuned on Jev's answers
for 1442 other prompts. The best of them serves 61 useful prompts top-1 where Jev serves 92–94. The trees match the
words' useful prompts with half their noise, which makes them a candidate for the setups that run without a judge.
They ship as the opt-in reader `MEMCUE_RERANK=local` ([#321](#shipped-as-local-321)).

## Setup

- Candidates: `cand.jsonl` of #274, 180 gold prompts on the gold base copy, 20 per prompt in BM25 order; 171 prompts
  have two or more candidates, 3377 pairs. Jev: the two runs of #274 and the third of the Luna note.
- Labels: Opus, blind, the `judge_ab_report.py` definitions. The 767 labels of earlier notes were extended to 1405 by
  labelling every pair any reader below serves at any reported threshold ($7.18 in four passes). Every number below
  has no unlabelled pair.
- Features ([rankfeat](scripts/rankfeat/main.go)): the candidates recomputed with `RecallJudge` on the same base and
  checked equal to the file, then per pair BM25 rank, gate verdict, coverage, matched terms, the rarest matched term's
  weight, coverage on content, keywords and queries separately, ticket match, length, keywords, queries, kind, scope,
  source, provisional, age, expiry, versions; per list the best coverage, the gap to the second, the coverage rank,
  the number of passed candidates; per prompt terms, known terms, length, ticket keys. The session's repository is not
  among them: the gold file keeps no tier.
- Trees ([local_ranker.py](scripts/local_ranker.py)): LightGBM, 300 trees of 7 leaves, five folds grouped by prompt,
  so every prompt is scored by a model that never saw it; Platt scaling inside the training folds against the Opus
  label. `tree-jev` regresses the mean Jev probability, `tree-mix` takes the Opus label where there is one and Jev
  elsewhere, `tree-opus` learns the Opus labels alone.
- Cross-encoder ([cross_encoder.py](scripts/cross_encoder.py)): `cross-encoder/mmarco-mMiniLMv2-L12-H384-v1`, the
  published ONNX files, ONNX Runtime on the CPU. Fine-tuned ([ce_finetune.py](scripts/ce_finetune.py)) for two epochs
  on 27 659 pairs of 1442 prompts outside the gold set with Jev's probability as a float BCE target, MPS, 39 minutes.

## Results on the gold set

Useful prompts top-1 / any, and what the reader served (at most two facts a prompt). Each row is the reader's best
trade at the named threshold; [the full tables](#all-thresholds) are below.

| reader | min | top-1 | any | USEFUL | RELATED | NOISE | nothing served |
|---|---|---|---|---|---|---|---|
| Jev (run 1) | 0.50 | 92 | 101 | 154 | 77 | 44 | 23 |
| Jev (run 1) | 0.70 | 84 | 91 | 139 | 46 | 12 | 58 |
| words, current gate | — | 45 | 54 | 76 | 62 | 66 | 60 |
| first two in BM25 order, no gate | — | 57 | 77 | 104 | 111 | 127 | 0 |
| `tree-jev` | 0.35 | 49 | 55 | 75 | 56 | 32 | 68 |
| `tree-jev` | 0.30 | 59 | 71 | 96 | 94 | 84 | 14 |
| `tree-mix` | 0.30 | 54 | 69 | 92 | 74 | 61 | 41 |
| `tree-opus` | 0.30 | 46 | 58 | 79 | 68 | 78 | 46 |
| cross-encoder as published, int8 | 0.10 | 42 | 46 | 65 | 56 | 42 | 76 |
| trees + cross-encoder score as a feature | 0.30 | 57 | 73 | 99 | 95 | 68 | 25 |
| cross-encoder fine-tuned on Jev | 0.40 | 61 | 69 | 101 | 91 | 48 | 41 |
| cross-encoder fine-tuned on Jev | 0.50 | 54 | 59 | 84 | 69 | 30 | 67 |

What it says:

1. **Ordering without reading has a low ceiling.** The best tree puts a useful fact first on 59 prompts, two more
   than plain BM25 order; Jev does it on 92. A tree sees which words matched, not what the fact says, and the
   gain over the rank it already has is small.
2. **Abstention is what the trees do buy.** At 0.35 `tree-jev` serves as many useful prompts as the words with the
   current gate (49 / 55 against 45 / 54) and half their noise (32 against 66). That threshold was picked on the same
   set it is reported on, so the trade is optimistic by an unknown amount.
3. **The Opus labels alone are too few and too skewed to learn from.** `tree-opus` is the weakest tree: its labels are
   the pairs earlier readers chose to serve. The Jev probability over every candidate is the better target.
4. **The published cross-encoder is weaker than the words.** Its scores sit near zero (median 0.003), as MemReranker
   warned, and it puts a useful fact first on 42 prompts. As one more feature of the trees it changes little.
5. **Fine-tuning on Jev helps most and is still far behind.** From 42 to 61 useful prompts top-1 with 1442 training
   prompts. Rank-DistiLLM and TWOLAR distil at 10 000–20 000 queries; memcue has no more prompts than these.

## More prompts (#320)

1442 prompts outside the gold set: every typed prompt of the Claude Code transcripts on the Mac and of `recall.log`,
deduplicated, gold prompts removed ([prompt_pool.py](scripts/prompt_pool.py)); candidates with `judgecand` on the gold
base; Jev scored them in one pass (median 0.49 s, 5.08M input tokens, about $0.2). They join every training fold of the
trees with the Jev probability as label; the gold set is still the only test.

| pool prompts | `tree-jev` at 0.35: top-1 / any, NOISE | `tree-mix` at 0.35: top-1 / any, NOISE |
|---|---|---|
| 0 | 49 / 55, 32 | 44 / 48, 31 |
| 250 | 46 / 52, 32 | 44 / 51, 26 |
| 500 | 49 / 54, 31 | 46 / 53, 26 |
| 1000 | 50 / 59, 27 | 50 / 58, 27 |
| 1442 | 48 / 57, 29 | 49 / 58, 28 |

The curve is flat from the start: eight times the data moves the trees by a few prompts either way. The limit is the
features, not the labels, so waiting for more live prompts will not bring the trees to Jev. The pool is what the
fine-tuned cross-encoder was trained on.

## Runtime (#317)

On an Apple M4 Pro (12 cores), twenty pairs of a prompt and its candidates in one batch:

| runtime | cold start | median | p90 | cgo |
|---|---|---|---|---|
| ONNX Runtime (Python), int8 arm64 | 324 ms | 155 ms | 258 ms | the C library |
| ONNX Runtime (Python), fp32 | 380 ms | 177 ms | 306 ms | the C library |
| `hugot` 0.8.3 native Go backend, fp32 | 343 ms | 2950 ms | 4373 ms | no |
| `hugot` 0.8.3 native Go backend, int8 | 234 ms | 8788 ms | 12 441 ms | no |

The `hugot` Go backend builds with `CGO_ENABLED=0` on darwin/arm64 and runs (a 22 MB binary), although its README
names only amd64-linux. It is too slow for a hook with a 10 s timeout; 30 prompts were timed. Its scores differ
from ONNX Runtime's by 0.03 on average and up to 0.58; the cause was not looked into. A usable cross-encoder therefore
needs ONNX Runtime through cgo or a sidecar, and with a fresh process per prompt it costs about half a second, which is
Jev's own latency. Nothing was timed on the Linux VPS.

The trees cost nothing at read time: `leaves` predicts a row in microseconds in pure Go.

## Decisions

- **#316, trees**: no replacement for Jev. They are the right reader for the setups that run without one
  (`MEMCUE_RERANK=off`, no judge key, the judge paused): the same useful prompts as the words with half the noise, no
  remote call, deterministic, in the static binary. Worth an implementation issue, with the threshold chosen on data
  apart from the gold set.
- **#320, more labels**: stop. The trees gain nothing from eight times more prompts. Keep `MEMCUE_RECALL_LOG=1` on
  the hosts, because the cross-encoder is the reader that does gain from prompts.
- **#317, cross-encoder**: not built in. As published it is weaker than the words; fine-tuned it is the best local
  reader and still 30 prompts behind Jev; and it needs cgo or a sidecar for half a second a prompt. Revisit when the
  prompt pool is several times larger.

## Shipped as `local` (#321)

The model memcue embeds is trained by [local_train.py](scripts/local_train.py) on the 1442 pool prompts alone, with
one Jev run as the target and no Platt scaling, so the gold set is unseen by it and nothing below was fitted on it.
recall hands the reader the features of [rankfeat](scripts/rankfeat/main.go) through `recall.FeatureRanker`;
rankfeat now records them through the same path and reproduces the features of the runs above on all 171 prompts.
`internal/localrank` builds the vector in Go and walks the trees with `leaves`; its test reproduces LightGBM's raw
score on every gold pair within 1e-9, and `RecallJudge` with the reader gave the same scores on all 171 prompts.

| `local` at | top-1 | any | USEFUL | RELATED | NOISE | nothing served |
|---|---|---|---|---|---|---|
| 0.34 | 56 | 71 | 99 | 110 | 96 | 9 |
| 0.40 | 54 | 66 | 89 | 100 | 71 | 22 |
| **0.50** | **47** | **55** | **69** | **58** | **30** | **64** |

0.34 is the threshold whose decisions agreed best with Jev's at 0.5 on out-of-fold pool predictions (F1 0.50); it
serves more useful prompts than the words with more noise (two of its pairs are unlabelled). memcue reads the model at
Jev's own 0.5, which the model learnt to predict: as many useful prompts as the words, 30 noise facts against 66. The
model is 240 KB of LightGBM text; with `leaves` it adds 332 KB to the stripped `CGO_ENABLED=0` binary (13.8 MB).

It is not the default. On a fresh base of two facts written by `memcue add` alone (no keywords, queries, kind or
source), the reader scored
"Проект demo: кеш Redis слушает порт 6379." at 0.49 for "demo redis порт кеш", a prompt whose every term the fact
holds, and served nothing; the words serve it. The trees learnt the gold base, whose facts are expanded and labelled,
and a base unlike it falls outside what they saw. How large or how expanded a base must be for the reader to help was
not measured.

## Limits

- One base, one user's prompts; 171 test prompts. A difference of a few prompts is noise.
- The thresholds of the local readers were read off the same set they are reported on.
- Only Jev's own ordering was distilled; a reader that copies Jev cannot beat it on these labels, and Jev's two runs
  already differ by two prompts.
- The cross-encoder was tuned once, with one set of hyperparameters.

## Reproducing

```sh
go run -tags research ./docs/research/scripts/rankfeat base.db cand.jsonl 2026-10-22 > feat.jsonl
python3 -I docs/research/scripts/local_ranker.py cand.jsonl feat.jsonl labels.json out jev-1.jsonl jev-2.jsonl jev-3.jsonl
python3 -I docs/research/scripts/cross_encoder.py MODEL_DIR onnx/model_qint8_arm64.onnx cand.jsonl ce-int8.jsonl
EXTRA=ce-int8.jsonl python3 -I docs/research/scripts/local_ranker.py cand.jsonl feat.jsonl labels.json out-ce jev-*.jsonl
python3 -I docs/research/scripts/prompt_pool.py cand.jsonl pool.json ~/.claude/projects --recall-log recall.log
go run -tags research ./docs/research/scripts/judgecand base.db pool.json > pool-cand.jsonl
python3 -I docs/research/scripts/judge_ab.py pool-cand.jsonl jev pool-jev.jsonl
POOL=pool-cand.jsonl:pool-feat.jsonl:pool-jev.jsonl POOL_N=1000 python3 -I docs/research/scripts/local_ranker.py ...
EPOCHS=2 python3 -I docs/research/scripts/ce_finetune.py MODEL_DIR pool-cand.jsonl pool-jev.jsonl cand.jsonl ce-tuned.jsonl
THRESHOLDS=... python3 -I docs/research/scripts/judge_ab_report.py label|report cand.jsonl labels.json READERS...
# the shipped model: internal/localrank/model.txt and testdata/gold.jsonl.gz, plus local.jsonl for the report
python3 -I docs/research/scripts/local_train.py pool-cand.jsonl pool-feat.jsonl pool-jev.jsonl feat.jsonl OUT
```

The trees and the cross-encoder need `lightgbm`, `scikit-learn`, `onnxruntime`, `tokenizers`, `torch` and
`transformers` in a virtual environment; LightGBM on macOS needs `brew install libomp`.

## All thresholds

```
run             min  top-1   any  USEFUL  RELATED  NOISE  none  unlab
jev-1          0.10     95   105     159       98     75     0     10
jev-1          0.30     94   104     158       96     67     5      0
jev-1          0.35     94   104     158       93     59     9      0
jev-1          0.40     94   104     157       90     52    11      0
jev-1          0.45     94   103     156       85     48    18      0
jev-1          0.50     92   101     154       77     44    23      0
jev-1          0.70     84    91     139       46     12    58      0
jev-1          0.90     31    31      46        4      0   139      0
jev-3          0.10     93   106     161       97     76     0      8
jev-3          0.30     92   105     160       95     68     5      0
jev-3          0.35     92   105     160       93     58     8      0
jev-3          0.40     92   105     160       90     51    11      0
jev-3          0.45     92   103     157       84     46    18      0
jev-3          0.50     92   103     157       76     39    24      0
jev-3          0.70     82    89     136       47     10    60      0
jev-3          0.90     31    32      48        3      0   138      0
words          0.10     45    54      76       62     66    60      0
words          0.30     45    54      76       62     66    60      0
words          0.35     45    54      76       62     66    60      0
words          0.40     45    54      76       62     66    60      0
words          0.45     45    54      76       62     66    60      0
words          0.50     45    54      76       62     66    60      0
words          0.70     45    54      76       62     66    60      0
words          0.90     45    54      76       62     66    60      0
bm25           0.10     57    77     104      111    127     0      0
bm25           0.30     57    77     104      111    127     0      0
bm25           0.35     57    77     104      111    127     0      0
bm25           0.40     57    77     104      111    127     0      0
bm25           0.45     57    77     104      111    127     0      0
bm25           0.50     57    77     104      111    127     0      0
bm25           0.70     57    77     104      111    127     0      0
bm25           0.90     57    77     104      111    127     0      0
tree-jev       0.10     63    80     109      112    121     0      0
tree-jev       0.30     59    71      96       94     84    14      0
tree-jev       0.35     49    55      75       56     32    68      0
tree-jev       0.40     31    35      44       27     12   116      0
tree-jev       0.45     19    22      27       14      4   139      0
tree-jev       0.50      8     8      10        3      1   160      0
tree-jev       0.70      0     0       0        0      0   171      0
tree-jev       0.90      0     0       0        0      0   171      0
tree-mix       0.10     62    84     110       99    133     0      0
tree-mix       0.30     54    69      92       74     61    41      0
tree-mix       0.35     44    48      66       46     31    83      0
tree-mix       0.40     35    38      55       29     13   112      0
tree-mix       0.45     27    29      40       15      7   131      0
tree-mix       0.50     23    23      30       10      0   143      0
tree-mix       0.70      3     3       4        0      0   168      0
tree-mix       0.90      0     0       0        0      0   171      0
tree-opus      0.10     53    68      93       96    153     0      0
tree-opus      0.30     46    58      79       68     78    46      0
tree-opus      0.35     33    41      55       41     37    95      0
tree-opus      0.40     27    31      40       25     19   120      0
tree-opus      0.45     18    20      27       12      8   141      0
tree-opus      0.50     12    14      16        4      3   152      0
tree-opus      0.70      0     0       0        0      0   171      0
tree-opus      0.90      0     0       0        0      0   171      0
tree-jev-ce    0.10     64    83     113      115    114     0      0
tree-jev-ce    0.30     57    73      99       95     68    25      0
tree-jev-ce    0.35     49    62      85       68     45    53      0
tree-jev-ce    0.40     38    44      61       40     18    95      0
tree-jev-ce    0.45     29    32      35       19      7   124      0
tree-jev-ce    0.50     13    13      16        4      2   153      0
tree-jev-ce    0.70      0     0       0        0      0   171      0
tree-jev-ce    0.90      0     0       0        0      0   171      0
tree-mix-ce    0.10     64    81     112      106    124     0      0
tree-mix-ce    0.30     55    68      91       79     59    41      0
tree-mix-ce    0.35     47    57      75       49     31    77      0
tree-mix-ce    0.40     39    44      59       27     16   105      0
tree-mix-ce    0.45     30    30      43       15      4   133      0
tree-mix-ce    0.50     26    26      34       13      4   139      0
tree-mix-ce    0.70      3     3       4        0      0   168      0
tree-mix-ce    0.90      0     0       0        0      0   171      0
ce-int8        0.10     42    46      65       56     42    76      0
ce-int8        0.30     29    31      46       29     22   113      0
ce-int8        0.35     28    30      44       24     20   118      0
ce-int8        0.40     26    28      40       23     19   121      0
ce-int8        0.45     23    25      35       22     19   125      0
ce-int8        0.50     21    23      33       19     16   130      0
ce-int8        0.70     19    20      28       12     12   138      0
ce-int8        0.90     11    11      18        5     10   151      0
ce-fp32        0.10     38    44      63       55     40    80      0
ce-fp32        0.30     29    32      47       27     23   114      0
ce-fp32        0.35     27    30      44       27     20   117      0
ce-fp32        0.40     24    27      41       23     19   121      0
ce-fp32        0.45     23    26      36       21     17   126      0
ce-fp32        0.50     21    24      34       20     16   129      0
ce-fp32        0.70     19    21      30       13     14   136      0
ce-fp32        0.90     13    14      21        6      9   148      0
ce-tuned       0.30     64    73     108      103     72    23      0
ce-tuned       0.40     61    69     101       91     48    41      0
ce-tuned       0.50     54    59      84       69     30    67      0
ce-tuned       0.60     46    49      68       43     19    94      0
ce-tuned       0.70     31    33      45       25      4   123      0
tree-jev-p250  0.30     55    67      88       83     60    30      0
tree-jev-p250  0.35     46    52      63       52     32    72      0
tree-jev-p250  0.40     27    30      35       21      8   121      0
tree-jev-p250  0.45     15    15      17        7      2   148      0
tree-jev-p250  0.50      4     4       4        0      0   167      0
tree-jev-p500  0.30     55    64      87       80     69    36      0
tree-jev-p500  0.35     49    54      69       58     31    65      0
tree-jev-p500  0.40     29    33      42       24      9   117      0
tree-jev-p500  0.45     20    21      25       12      3   140      0
tree-jev-p500  0.50      8     8       9        4      1   159      0
tree-jev-p1000 0.30     55    66      87       86     63    33      0
tree-jev-p1000 0.35     50    59      74       63     27    65      0
tree-jev-p1000 0.40     37    38      47       33     10   102      0
tree-jev-p1000 0.45     26    27      33       14      3   132      0
tree-jev-p1000 0.50     11    11      11        7      1   153      0
tree-jev-p1442 0.30     54    66      88       84     61    36      0
tree-jev-p1442 0.35     48    57      71       60     29    63      0
tree-jev-p1442 0.40     34    37      46       35     10   102      0
tree-jev-p1442 0.45     23    24      30       14      4   135      0
tree-jev-p1442 0.50     11    11      12        5      1   154      0
tree-mix-p250  0.30     57    68      90       80     56    34      0
tree-mix-p250  0.35     44    51      65       49     26    78      0
tree-mix-p250  0.40     28    31      38       25     10   120      0
tree-mix-p250  0.45     21    21      25       11      4   138      0
tree-mix-p250  0.50     10    10      10        3      0   159      0
tree-mix-p500  0.30     54    65      85       81     57    37      0
tree-mix-p500  0.35     46    53      69       58     26    73      0
tree-mix-p500  0.40     28    32      41       28     10   115      0
tree-mix-p500  0.45     17    18      23       12      3   143      0
tree-mix-p500  0.50      8     8       8        3      1   160      0
tree-mix-p1000 0.30     54    65      88       81     60    38      0
tree-mix-p1000 0.35     50    58      74       61     27    68      0
tree-mix-p1000 0.40     37    39      50       33     10   102      0
tree-mix-p1000 0.45     26    27      34       18      3   130      0
tree-mix-p1000 0.50     13    13      15        5      1   154      0
tree-mix-p1442 0.30     55    66      87       87     58    36      0
tree-mix-p1442 0.35     49    58      73       59     28    67      0
tree-mix-p1442 0.40     37    41      51       36     13    98      0
tree-mix-p1442 0.45     24    24      30       16      4   132      0
tree-mix-p1442 0.50     12    12      14        6      2   153      0
```
