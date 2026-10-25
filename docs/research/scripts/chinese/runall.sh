#!/bin/bash
# runall.sh: every base x lexer x query set of #410; one jsonl per run in runs/, scores in runs/scores.txt.
cd "$(dirname "$0")"
run() { # db mode bin set
  out=runs/$1--$4.jsonl
  ./$3 search db/$1.db q/$4.json "$2" 20 > $out 2> $out.err || echo "FAIL $out"
  python3 score.py truth.json $out "$1 $4"
}
: > runs/scores.txt
for s in zh ruen zh2ru zh2en pure pure2ru pure2en; do run full-off "" zhstand $s; run subset-off "" zhstand $s; done >> runs/scores.txt
for s in zh ruen pure; do
  run full-bi bigram zhstand $s
  run subset-bi bigram zhstand $s
  run subset-zh-off "" zhstand $s
  run subset-zh-bi bigram zhstand $s
  run subset-zh-gse gse zhstand-gse $s
done >> runs/scores.txt
