"""pick_subset.py: the 60 targets plus 240 nearest BM25 competitors, by best rank in the no-floor top 10 of the
ru/en/zh questions on the full base (runs/full-off.jsonl). Ties: more questions that rank the fact, then id."""
import json
truth=json.load(open('truth.json')); rows=[json.loads(l) for l in open('runs/full-off.jsonl')]
targets=sorted({truth[r['id']]['target'] for r in rows})
best={};cnt={}
for r in rows:
    for i,f in enumerate(r['top10']):
        if f in targets: continue
        best[f]=min(best.get(f,99),i); cnt[f]=cnt.get(f,0)+1
others=sorted(best,key=lambda f:(best[f],-cnt[f],f))
print('candidates',len(others))
pick=others[:240]
print('worst rank taken',best[pick[-1]], 'by rank', {k:sum(best[f]==k for f in pick) for k in range(10)})
json.dump({'targets':targets,'others':pick},open('subset.json','w'))
