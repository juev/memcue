import json,sys,re,random
import numpy as np
from pathlib import Path
cands, opus, d = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3])
rows=[json.loads(l) for l in cands.open()]
ids=[r['fact']['id'] for r in rows]; txt={r['fact']['id']:r['fact'] for r in rows}; ix={x:i for i,x in enumerate(ids)}
v=np.load(d/'emb.npy'); v/=np.linalg.norm(v,axis=1,keepdims=True)
o=json.load(opus.open()); ul=re.compile(r'\b01[0-9A-HJKMNP-TV-Z]{24}\b')
P={frozenset((a['id'],x)) for a in o['actions'] for x in ul.findall(a['reason']) if x!=a['id']}
cs=sorted(float(v[ix[a]]@v[ix[b]]) for a,b in map(sorted,P))
print('opus pair cosine: min',round(cs[0],2),'p10',round(cs[6],2),'median',round(cs[30],2),'max',round(cs[-1],2))
vo=json.load((d/'vec_only.json').open())
cyr=lambda s: bool(re.search('[а-яА-Я]',s))
vo=[p for p in vo if p['cos']>=0.72]
mixed=[p for p in vo if cyr(txt[p['a']]['text'])!=cyr(txt[p['b']]['text'])]
print('vector-only cos>=0.72:',len(vo),'of them RU/EN mixed:',len(mixed))
random.seed(3)
for i,p in enumerate(sorted(random.sample(vo,10),key=lambda p:-p['cos']),1):
    print(f"{i}. {p['cos']:.2f}")
    for x in (p['a'],p['b']): f=txt[x]; print('   -',f['origin'].split(' (')[0],'|',f['text'][:140])
