import json,sys,random,collections
from pathlib import Path
d=Path(sys.argv[1])
pairs={p['n']:p for p in json.load((d/'pairs.json').open())}
lab={int(k):v for k,v in json.load((d/'labels.json').open()).items()}
jev={r['n']:r for r in json.load((d/'jev114.json').open())}
print('labels', collections.Counter(v['label'] for v in lab.values()))
print('by source:')
for s in sorted({p['source'] for p in pairs.values()}):
    c=collections.Counter(lab[n]['label'] for n,p in pairs.items() if p['source']==s)
    print(f'  {s:18} {dict(c)}')
def auc(sc,y):
    pos=[s for s,t in zip(sc,y) if t]; neg=[s for s,t in zip(sc,y) if not t]
    return sum((p>q)+0.5*(p==q) for p in pos for q in neg)/(len(pos)*len(neg))
ns=sorted(pairs); y=[lab[n]['label']!='different' for n in ns]
ydup=[lab[n]['label']=='duplicate' for n in ns]; yout=[lab[n]['label']=='outdated' for n in ns]
same=[jev[n]['same'] for n in ns]; contra=[jev[n]['contra'] for n in ns]; mx=[max(a,b) for a,b in zip(same,contra)]
cos=[pairs[n]['vec'] if pairs[n]['vec'] is not None else 0 for n in ns]
print(f"AUC any action: jev max {auc(mx,y):.3f}, jev same {auc(same,y):.3f}, cosine(top10 else 0) {auc(cos,y):.3f}")
if any(yout): print(f"AUC outdated: jev contra {auc(contra,yout):.3f}; AUC duplicate: jev same {auc(same,ydup):.3f}")
# threshold on half, test on other half
random.seed(1); idx=list(range(len(ns))); random.shuffle(idx); h=len(idx)//2; tune,test=idx[:h],idx[h:]
def stats(ix,t):
    tp=sum(1 for i in ix if mx[i]>=t and y[i]); fp=sum(1 for i in ix if mx[i]>=t and not y[i]); fn=sum(1 for i in ix if mx[i]<t and y[i])
    return tp,fp,fn
best=None
for t in [x/100 for x in range(5,96,5)]:
    tp,fp,fn=stats(tune,t)
    if tp+fn and fn<=0.1*(tp+fn):  # keep at least 90% of positives on the tune half
        sent=tp+fp; 
        if best is None or sent<best[1]: best=(t,sent)
print('tune positives',sum(y[i] for i in tune),'test positives',sum(y[i] for i in test))
if best:
    t=best[0]; tp,fp,fn=stats(test,t)
    print(f'threshold {t} (>=90% recall on tune half): test recall {tp}/{tp+fn}, sent on {tp+fp}/{len(test)}, precision {tp/(tp+fp) if tp+fp else 0:.2f}')
for t in (0.3,0.5,0.7):
    tp,fp,fn=stats(idx,t); print(f'  all pairs t={t}: recall {tp}/{tp+fn}, sent {tp+fp}/{len(idx)}, precision {tp/(tp+fp) if tp+fp else 0:.2f}')
