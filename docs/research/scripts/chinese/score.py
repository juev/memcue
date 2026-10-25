"""score.py TRUTH RUN.jsonl [label] -- hit@5 (floor), rank 1, empty, no terms, mean latency per language."""
import json,sys,statistics as st
truth=json.load(open(sys.argv[1])); rows=[json.loads(l) for l in open(sys.argv[2])]
label=sys.argv[3] if len(sys.argv)>3 else sys.argv[2]
out={}
for lang in ('ru','en','zh'):
    rs=[r for r in rows if truth.get(r['id'],{}).get('lang')==lang or r['id'].endswith('|'+lang)]
    if not rs: continue
    tg=lambda r: truth[r['id']]['target'] if r['id'] in truth else r['id'].split('|')[1]
    h5=sum(tg(r) in (r['top5'] or []) for r in rs)
    r1=sum(bool(r['top5']) and r['top5'][0]==tg(r) for r in rs)
    h10=sum(tg(r) in (r['top10'] or []) for r in rs)
    empty=sum(not r['top5'] for r in rs); noterm=sum(not r['terms'] for r in rs)
    lat=st.mean(r['lat_us'] for r in rs)
    out[lang]=dict(n=len(rs),hit5=h5,rank1=r1,hit10=h10,empty=empty,noterms=noterm,lat_us=round(lat))
print(label, json.dumps(out))
