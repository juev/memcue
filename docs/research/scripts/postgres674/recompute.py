"""Independently recompute metrics and invariants from saved raw ranks (no DB/model)."""
import argparse,hashlib,json,math,statistics
from pathlib import Path
from outputs import write_new_text
HERE=Path(__file__).resolve().parent

def quantile(values,q):
    v=sorted(values)
    # nearest rank; 10 cold samples cannot estimate a stable p99 tail
    return v[max(0,math.ceil(q*len(v))-1)]
def summarize(samples):
    return dict(n=len(samples),p50=quantile(samples,.5),p95=quantile(samples,.95),p99=quantile(samples,.99))
def metrics(rows,cases,k):
    hits=[];recalls=[];precision=[];harm=[];missing=[]
    for r in rows:
        c=cases[r['case']];top=[h['id'] for h in r['hits'][:k]];targets=set(c['targets']);useful=len(targets.intersection(top))
        hits.append(int(useful>0));recalls.append(useful/len(targets));precision.append(useful/len(top) if top else 0)
        harm.append(len(set(c['harmful']).intersection(top)));missing.append(len(targets-set(top)))
    return dict(n=len(rows),target_hit=statistics.mean(hits),target_recall=statistics.mean(recalls),strict_useful_precision=statistics.mean(precision),known_harmful_count=sum(harm),missing_target_count=sum(missing))

def eligible(f,fl):
    if f['status']!='active': return False
    if fl.get('Today') and f['expires'] and f['expires']<fl['Today']: return False
    if fl.get('Since') and f['created'][:19]<fl['Since']: return False
    if fl.get('Kinds') and f['kind'] not in fl['Kinds']: return False
    scope=f['scope'].lower()
    include=[s.lower() for s in fl.get('Scopes') or []];prefixes=[p.lower() for p in fl.get('ScopePrefixes') or []];origins=fl.get('Origins') or []
    if include or prefixes or origins:
        if not (scope in include or any(scope.startswith(p) for p in prefixes) or any(f['origin'].startswith(o+' ') for o in origins)): return False
    if any(scope.startswith(p.lower()) for p in fl.get('ExcludePrefixes') or []) and scope not in [s.lower() for s in fl.get('KeepScopes') or []]: return False
    return True

def recompute(out):
    fixture=json.loads((HERE/'fixture.json').read_text());cases={x['id']:x for x in fixture['cases']};latest={f['id']:f for f in fixture['facts']}
    freeze=json.loads((out/'freeze.json').read_text())
    assert hashlib.sha256((HERE/'fixture.json').read_bytes()).hexdigest()==freeze['inputs']['fixture.json']
    assert hashlib.sha256((HERE/'declaration.json').read_bytes()).hexdigest()==freeze['inputs']['declaration.json']
    normalized=json.loads((out/'sqlite/normalized.json').read_text());norms={x['id']:x for x in normalized['cases']}
    rows=json.loads((out/'sqlite/sqlite-results.json').read_text())+json.loads((out/'pg-results.json').read_text())
    arms=sorted({x['arm'] for x in rows});assert len(arms)==5
    expected={(a,c,r) for a in arms for c in cases for r in [0,1]}
    assert len(rows)==len(expected) and {(x['arm'],x['case'],x['rep']) for x in rows}==expected
    for a in arms:
        for c in cases:
            runs=[x for x in rows if x['arm']==a and x['case']==c]
            assert runs[0]['hits']==runs[1]['hits'],('baseline/repetition mismatch',a,c)
    leaks=[]
    for row in rows:
        for hit in row['hits']:
            f=latest[hit['id']]
            if hit['version']!=f['version'] or not eligible(f,norms[row['case']]['filter']): leaks.append([row['arm'],row['case'],hit['id']])
    assert not leaks,leaks
    for row in rows:
        if cases[row['case']]['split']=='mechanics' and row['arm'] in ['sqlite-content','pg-lexical']: assert row['hits']==[],('field isolation',row)
    # Full-index positive controls show that the markers were actually indexed.
    for c in ['aux-en','query-en']:
        assert any(x['arm']=='sqlite-full' and x['case']==c and any(h['id']=='aux-only' for h in x['hits']) for x in rows)
    integrity=json.loads((out/'integrity.json').read_text());assert integrity['sqlite_history_equal'] and integrity['pg_remaining_fields_equal_frozen_history'] and integrity['pg_history_unchanged_after_retrieval']
    projected=sorted([{k:f[k] for k in 'id version content origin created status author expires kind scope source evidence review prompt_version channel'.split()} for f in fixture['facts']],key=lambda f:(f['id'],f['version']))
    assert json.loads((out/'pg-history.json').read_text())==projected
    vectors=json.loads((out/'vectors.json').read_text());assert all(len(v)==384 for v in vectors['facts'].values())
    failures=json.loads((out/'failure-results.json').read_text());assert all(x['passed'] for x in failures)
    result=dict(synthetic=True,local_only=True,fixture_sha256=freeze['inputs']['fixture.json'],held_out_cases=sum(c['split']=='held-out' for c in cases.values()),source_history_rows=len(fixture['facts']),logical_facts=len(latest),retrieval_requests=len(rows),repetition_rank_equality=True,filter_leaks=len(leaks),failure_assertions=len(failures),quality={},strata={},latency={},embedding={},unknown=dict(actual_agent_effectiveness=None,remote_path_latency=None,provider_usage=None,provider_cost=None,production_quality=None))
    for a in arms:
        armrows=[r for r in rows if r['arm']==a and r['rep']==0 and cases[r['case']]['split']=='held-out']
        result['quality'][a]={str(k):metrics(armrows,cases,k) for k in [1,5,10]}
        result['strata'][a]={}
        for field in ['language','stratum']:
            result['strata'][a][field]={}
            for value in sorted({cases[r['case']][field] for r in armrows}):
                subset=[r for r in armrows if cases[r['case']][field]==value]
                result['strata'][a][field][value]={str(k):metrics(subset,cases,k) for k in [1,5,10]}
    transport=json.loads((out/'transport-results.json').read_text())
    for route in ['wire','http']:
        for phase in ['cold-cli','warm-session']:
            samples=[x for x in transport if x['route']==route and x['phase']==phase]
            assert len(samples)==(10 if phase=='cold-cli' else 30)
            result['latency'][route+'-'+phase]={key:summarize([x[key] for x in samples]) for key in ['end_to_end_ms','sql_ms','embed_ms']}
    times=json.loads((out/'embedding-times.json').read_text())
    for kind in ['passage','query']: result['embedding'][kind]=summarize([x['ms'] for x in times if x['kind']==kind])
    content=result['quality']['sqlite-content']['5']['target_hit']
    result['thresholds']={a:dict(synthetic_hit_at_5_nonregression=result['quality'][a]['5']['target_hit']>=content-.05,identifier_number_hit_at_1=sum(result['strata'][a]['stratum'][s]['1']['target_hit']*result['strata'][a]['stratum'][s]['1']['n'] for s in ['identifier','number'])/4>=.95) for a in arms}
    result['thresholds']['production_go']='UNVERIFIED: synthetic evidence cannot satisfy private-corpus, agent or remote gates'
    return result

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('evidence');p.add_argument('--output');a=p.parse_args();r=recompute(Path(a.evidence))
    value=json.dumps(r,ensure_ascii=False,indent=2)+'\n'
    if a.output: write_new_text(a.output,value)
    else: print(value,end='')
