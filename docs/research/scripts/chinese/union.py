"""union.py A.jsonl B.jsonl: target in the top 5 of either search (the agent runs both)."""
import json,sys
def load(f): return {json.loads(l)['id']:json.loads(l) for l in open(f)}
a,b=load(sys.argv[1]),load(sys.argv[2])
print(sys.argv[1],'+',sys.argv[2],'either:',sum(k.split('|')[1] in (a[k]['top5'] or [])+(b[k]['top5'] or []) for k in a),'/',len(a))
