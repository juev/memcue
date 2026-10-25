"""Apply candidate floors offline to the hits searchfloor wrote (#407).

usage: python3 eval.py DIR

DIR holds truth.json (what each query is: known item, version-only or agent query), hits.jsonl and agent-hits.jsonl
(searchfloor output) and the labels of the agent queries' facts, alab-*.txt.out, one JSON line per fact:
{"prompt": ..., "fact": ..., "label": "U|R|N"}. Experiment only, not part of memcue.
"""
import json,sys,glob
F=sys.argv[1]
truth=json.load(open(F+'/truth.json'))
rows={}
for f in ('hits.jsonl','agent-hits.jsonl'):
    for l in open(F+'/'+f): r=json.loads(l); rows[r['id']]=r
lab={}
for f in glob.glob(F+'/lab-*.txt.out')+glob.glob(F+'/alab-*.txt.out'):
    for l in open(f):
        l=l.strip()
        if l.startswith('{'): d=json.loads(l); lab[(d['prompt'],d['fact'])]=d['label']
def arm(name,keep,N=5):
    known={};vers=0;agent=dict(U=0,R=0,N=0,pu=0,empty=0,lostU=0,facts=0,chars=0)
    for pid,r in rows.items():
        t=truth[pid]; hs=[h for h in r['hits'][:N] if keep(h)]
        if t['kind']=='known':
            known[t['lang']]=known.get(t['lang'],0)+any(h['id']==t['target'] for h in hs)
        elif t['kind']=='version':
            vers+=any(t['v'] in h['content'] for h in hs)
        elif t['kind']=='agent':
            ls=[lab.get((pid,h['id']),'?') for h in hs]
            for k in 'URN': agent[k]+=ls.count(k)
            agent['pu']+='U' in ls; agent['facts']+=len(hs); agent['chars']+=sum(len(h['content']) for h in hs)
            base=[lab.get((pid,h['id'])) for h in r['hits'][:N]]
            if not hs: agent['empty']+=1
            if 'U' in base and 'U' not in ls: agent['lostU']+=1
    a=agent; tot=a['U']+a['R']+a['N'] or 1
    print(f"{name:20} known {known.get('ru')}/{known.get('en')}/{known.get('zh')} vers {vers} | agent 35: w/U {a['pu']} lostU-prompts {a['lostU']} U {a['U']} R {a['R']} N {a['N']} noise {a['N']/tot:.0%} useful {a['U']/tot:.0%} empty {a['empty']} facts {a['facts']/35:.2f} chars {a['chars']/35:.0f}")
arm('top5 (now)',lambda h:True)
arm('top10',lambda h:True,10)
for r in (0.4,0.5,0.6,0.7,0.8): arm(f'ratio>={r}',lambda h,r=r:h['ratio']>=r)
for c in (0.3,0.4,0.5,0.6,0.7): arm(f'coverage>={c}',lambda h,c=c:h['coverage']>=c)
arm('matched>=2',lambda h:h['matched']>=2)
for r in (0.5,0.6):
  for c in (0.4,0.5): arm(f'r>={r}&c>={c}',lambda h,r=r,c=c:h['ratio']>=r and h['coverage']>=c)
