#!/bin/bash
# lat.sh: latency pass, 100 repetitions per query, two rounds in alternating order.
cd "$(dirname "$0")"
for round in 1 2; do
for v in "full-off||zhstand" "full-bi|bigram|zhstand" "subset-off||zhstand" "subset-zh-off||zhstand" "subset-zh-bi|bigram|zhstand" "subset-zh-gse|gse|zhstand-gse"; do
  IFS='|' read db mode bin <<< "$v"
  for s in zh ruen pure; do
    ./$bin search db/$db.db q/$s.json "$mode" 100 > lat/$db--$s--$round.jsonl
    python3 -c "
import json,statistics as st,sys
r=[json.loads(l)['lat_us'] for l in open('lat/$db--$s--$round.jsonl')]
print('$round $db $s mean_us',round(st.mean(r)),'median_us',round(st.median(r)))"
  done
done; done
