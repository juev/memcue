"""Independent saved-record audit. No driver/backend/model/database imports.

Only fixed arm/language/phase names become public aggregate keys. Raw inputs,
queries, identities, hosts, scopes and arbitrary labels never leave this file.
"""
import argparse
import collections
import contextlib
import hashlib
import json
import math
from pathlib import Path
import re
import statistics
from outputs import write_new_text

ARMS=('sqlite-full','sqlite-content','pg-lexical','pg-vector-exact','pg-hybrid-rrf60')
LANGUAGES=('latin','cyrillic','other')


def read(path):return json.loads(path.read_text())
def lines(path):return [json.loads(line) for line in path.read_text().splitlines() if line.strip()]
def quantiles(values):
    values=sorted(values)
    if not values:return dict(n=0,p50=None,p95=None,p99=None)
    return dict(n=len(values),**{name:values[max(0,math.ceil(p*len(values))-1)] for name,p in [('p50',.5),('p95',.95),('p99',.99)]})


def eligible(row,flt):
    if row['status']!='active':return False
    if flt.get('Today') and row['expires'] and row['expires']<flt['Today']:return False
    if flt.get('Kinds') and row['kind'] not in flt['Kinds']:return False
    if flt.get('Since') and row['created'][:19]<flt['Since']:return False
    scope=row['scope'].lower()
    alternatives=[]
    for item in flt.get('Scopes') or []:alternatives.append(scope==item.lower())
    for item in flt.get('ScopePrefixes') or []:alternatives.append(scope.startswith(item.lower()))
    for item in flt.get('Origins') or []:alternatives.append(row['origin'].startswith(item+' '))
    if alternatives and not any(alternatives):return False
    for item in flt.get('ExcludePrefixes') or []:
        if scope.startswith(item.lower()) and scope not in [s.lower() for s in flt.get('KeepScopes') or []]:return False
    return True


def native_created(value):
    # The frozen writer uses UTC RFC3339; Go's Nano formatting strips trailing zeros.
    assert value.endswith('Z'),'unexpected timestamp route; independent audit must be extended explicitly'
    if '.' in value:value=value[:-1].rstrip('0').rstrip('.')+'Z'
    return value


def audit_source(root, inputs):
    fixture=read(root/'native/fixture.json')['facts'];expected={};line_count=0
    defaults=dict(keywords=[],queries=[],origin='',author='',expires='',kind='',scope='',source='',evidence='',review='',prompt_version=0,channel='')
    paths=sorted((inputs/'facts').rglob('*.jsonl'))
    for path in paths:
        for line in path.read_text().splitlines():
            if not line.strip():continue
            line_count+=1;row=dict(defaults,**json.loads(line));row['created']=native_created(row['created'])
            row['keywords']=', '.join(row['keywords']);row['queries']=', '.join(row['queries'])
            row={key:row[key] for key in fixture[0]}
            key=(row['id'],row['version'])
            if key in expected:assert expected[key]==row,'conflicting duplicate in source'
            expected[key]=row
    assert sorted(expected.values(),key=lambda f:(f['id'],f['version']))==fixture,'native parser field mismatch'
    manifest=read(inputs/'manifest.json')
    log_records=lines(inputs/'search.log')
    assert manifest['fact_files']==len(paths) and manifest['search_log_records']==len(log_records)
    assert hashlib.sha256((inputs/'manifest.json').read_bytes()).hexdigest()==read(root/'freeze.json')['input_manifest_sha256']
    for name,h in manifest['sha256'].items():assert hashlib.sha256((inputs/name).read_bytes()).hexdigest()==h
    latest={}
    for row in expected.values():
        if row['version']>latest.get(row['id'],{}).get('version',0):latest[row['id']]=row
    pg=read(root/'pg-history.json')
    assert pg==[{k:v for k,v in row.items() if k not in ('keywords','queries')} for row in fixture]
    for arm in ARMS[:2]:
        rows=read(root/'sqlite'/f'{arm}-history.json')
        assert len(rows)==len(expected)
        for row in rows:
            f=expected[(row['ID'],row['Version'])]
            fields={'ID':'id','Version':'version','Content':'content','Origin':'origin','Created':'created','Status':'status',
                    'Author':'author','Expires':'expires','Kind':'kind','Scope':'scope','Source':'source','Evidence':'evidence',
                    'Review':'review','PromptVersion':'prompt_version','Channel':'channel'}
            for go,key in fields.items():assert row[go]==f[key],key
            for go,key in [('Keywords','keywords'),('Queries','queries')]:assert ', '.join(row.get(go) or [])==f[key]
    active=sum(f['status']=='active' for f in latest.values())
    expired=sum(f['status']=='active' and bool(f['expires']) and f['expires']<'2026-11-06' for f in latest.values())
    return latest,dict(input_files=len(manifest['sha256']),fact_files=len(paths),log_records=len(log_records),
        raw_version_lines=line_count,unique_version_rows=len(expected),duplicate_lines=line_count-len(expected),unique_ids=len(latest),latest_active=active,latest_active_expired=expired)


def main():
    parser=argparse.ArgumentParser();parser.add_argument('evidence',type=Path);parser.add_argument('--inputs',type=Path,required=True);parser.add_argument('--output',type=Path,required=True)
    args=parser.parse_args();root=args.evidence
    assert read(root/'complete.json')['source_unchanged']
    frozen=read(root/'freeze.json')
    for name,h in frozen['source_hashes'].items():assert hashlib.sha256((root/'source'/name).read_bytes()).hexdigest()==h
    latest,counts=audit_source(root,args.inputs)
    cases={c['id']:c for c in read(root/'cases.json')};norms={c['id']:c for c in read(root/'sqlite/normalized.json')['cases']}
    assert collections.Counter(c['language'] for c in cases.values())==dict(latin=64,cyrillic=63,other=1)
    sqlite=read(root/'sqlite/sqlite-results.json');pg=lines(root/'pg-results.jsonl');cli=lines(root/'cli-results.jsonl')
    for arm in ARMS[:2]:
        a={r['case']:r['hits'] for r in sqlite if r['arm']==arm and r['rep']==0}
        b={r['case']:r['hits'] for r in sqlite if r['arm']==arm and r['rep']==1};assert a==b
    invariant_hits=0
    for row in sqlite+pg+cli+read(root/'floor-results.json'):
        assert row['arm'] in ARMS and row['case'] in cases
        assert len(row['hits'])<=10 and len({h['id'] for h in row['hits']})==len(row['hits'])
        for hit in row['hits']:
            f=latest[hit['id']];assert hit['version']==f['version'] and eligible(f,norms[row['case']]['filter'])
            if 'content' in hit:assert hit['content']==f['content']
            invariant_hits+=1
    # Independent candidate-index audit using SQLite's read-only C API through stdlib.
    import sqlite3
    normalized=read(root/'sqlite/normalized.json')
    index_counts={}
    for arm in ARMS[:2]:
        with contextlib.closing(sqlite3.connect('file:'+str((root/'sqlite'/(arm+'.db')).absolute())+'?mode=ro',uri=True)) as db:
            rows=db.execute('SELECT i.id,f.version,i.stems FROM fact_index i JOIN facts f ON f.id=i.id AND f.version=(SELECT max(version) FROM facts z WHERE z.id=i.id)').fetchall();index_counts[arm]=len(rows)
            assert len(rows)==counts['latest_active']
            if arm=='sqlite-content':
                for id,version,stems in rows:assert version==latest[id]['version'] and stems==normalized['stems'][id]
            assert db.execute('PRAGMA integrity_check').fetchone()==('ok',)
    after=read(root/'deployment-after.json');assert after['history_unchanged'] and after['existing_relations_unchanged']
    assert all(r['owner']==after['preflight']['identity']['current_user'] for r in after['relations'])
    reference={(r['arm'],r['case']):[(h['id'],h['version']) for h in r['hits']] for r in sqlite+pg if r.get('rep')==0 and r.get('concurrency',1)==1}
    for row in pg+cli:
        assert [(h['id'],h['version']) for h in row['hits']]==reference[(row['arm'],row['case'])], 'repetition or CLI ranks changed'
    assert after['counts']==dict(facts=counts['unique_version_rows'],fact_index=counts['latest_active'],fact_fts=counts['latest_active'],fact_vectors=counts['latest_active'],stamps=0,scale=30000)
    for v in after['vector_rows']:
        f=latest[v['id']];assert f['status']=='active' and v['version']==f['version'] and v['dim']==384 and v['tag']==frozen['model_tag'] and v['text_hash']==hashlib.sha256(f['content'].encode()).hexdigest()
    # Exact vectors use saved genuine embeddings; this is not a semantic label oracle.
    import numpy as np
    vectors=read(root/'vectors.json');ids=sorted(vectors['facts']);matrix=np.asarray([vectors['facts'][id] for id in ids],dtype=np.float64)
    matrix/=np.linalg.norm(matrix,axis=1)[:,None]
    exact_checked=0
    for c in cases.values():
        q=np.asarray(vectors['queries'][c['id']],dtype=np.float64);q/=np.linalg.norm(q)
        scores=matrix@q;allowed=[i for i,id in enumerate(ids) if eligible(latest[id],norms[c['id']]['filter'])]
        oracle=sorted((float(scores[i]) for i in allowed),reverse=True)[:10]
        reference=None
        for r in pg:
            if r['case']!=c['id'] or r['arm']!='pg-vector-exact':continue
            actual=[h['score'] for h in r['hits']]
            assert len(actual)==len(oracle) and all(abs(a-b)<3e-6 for a,b in zip(actual,oracle)),'exact filtered vector scores differ'
            ranked=[(h['id'],h['version']) for h in r['hits']]
            if reference is None:reference=ranked
            else:assert reference==ranked,'PG exact repetitions changed'
            exact_checked+=1
    representative=sqlite+ [r for r in pg if r['concurrency']==1 and r['rep']==0]
    selected={(r['arm'],r['case']):r for r in representative if r.get('rep',0)==0}
    retrieval={}
    for arm in ARMS:
        retrieval[arm]={}
        for category in ('all',*LANGUAGES):
            group=[c for c in cases.values() if category=='all' or c['language']==category]
            rows=[selected[(arm,c['id'])] for c in group]
            stats=dict(contexts=len(group),empty=sum(not r['hits'] for r in rows),returned_count=sum(len(r['hits']) for r in rows),
                       primary_characters=sum(len(latest[h['id']]['content']) for r in rows for h in r['hits']))
            for baseline in ARMS[:2]:
                for k in (1,5,10):
                    intersections=[];denominators=[];equal=0
                    for c,row in zip(group,rows):
                        a=[(h['id'],h['version']) for h in row['hits'][:k]];b=[(h['id'],h['version']) for h in selected[(baseline,c['id'])]['hits'][:k]]
                        intersections.append(len(set(a)&set(b)));denominators.append(len(b));equal+=a==b
                    stats[baseline+'-at-'+str(k)]=dict(intersection_count=sum(intersections),baseline_returned_count=sum(denominators),
                        overlap_fraction=(sum(intersections)/sum(denominators) if sum(denominators) else None),ranked_equal_contexts=equal)
            retrieval[arm][category]=stats
    latency={}
    for arm in ARMS:
        latency[arm]={}
        if arm in ARMS[:2]:latency[arm]['native-library-c1']=quantiles([r['sql_and_recall_ms'] for r in sqlite if r['arm']==arm])
        else:
            for workers in (1,2):latency[arm]['warm-sql-c'+str(workers)]=quantiles([r['sql_ms'] for r in pg if r['arm']==arm and r['concurrency']==workers])
        for workers in (1,2):
            rows=[r for r in cli if r['arm']==arm and r['concurrency']==workers and r['phase']=='new-process-vector-cache']
            latency[arm]['research-cli-cache-c'+str(workers)]=quantiles([r['elapsed_ms'] for r in rows])
            latency[arm]['research-cli-cache-c'+str(workers)]['response_bytes']=sum(r['response_bytes'] for r in rows)
        rows=[r for r in cli if r['arm']==arm and r['phase']=='new-process-model-load']
        if rows:
            latency[arm]['research-cli-cold-model']=quantiles([r['elapsed_ms'] for r in rows])
            latency[arm]['model-load']=quantiles([r['model_load_ms'] for r in rows])
            latency[arm]['query-embed']=quantiles([r['embed_ms'] for r in rows])
    embedding=lines(root/'embedding-times.jsonl');embedding_stats={}
    for kind,field in [('passage','encode_ms'),('query','encode_ms'),('query-cache','cache_ms')]:
        rows=[r for r in embedding if r['kind']==kind];embedding_stats[kind]=dict(quantiles=quantiles([r[field] for r in rows]),
            total_ms=sum(r[field] for r in rows),cache_hits=sum(r.get('cache_hit',kind=='query-cache') for r in rows))
    embedding_stats['passage']['at_token_limit']=sum(r.get('truncated',False) for r in embedding)
    scale=lines(root/'scale-results.jsonl');scale_stats=[]
    for size in (10000,30000):
        for arm in ARMS[2:4]:
            for workers in (1,2):
                rows=[r for r in scale if r['size']==size and r['arm']==arm and r['concurrency']==workers]
                assert len(rows)==24
                scale_stats.append(dict(size=size,arm=arm,concurrency=workers,sql_ms=quantiles([r['sql_ms'] for r in rows]),empty=sum(not r['hits'] for r in rows)))
    workload=lines(root/'workloads.jsonl');throughput=[]
    for phase in ('warm-session','research-cli','controlled-scale'):
        for arm in ARMS:
            for workers in (1,2):
                for size in ((10000,30000) if phase=='controlled-scale' else (None,)):
                    rows=[r for r in workload if r['phase']==phase and r['arm']==arm and r['concurrency']==workers and r.get('size')==size]
                    if not rows:continue
                    throughput.append(dict(phase=phase,arm=arm,concurrency=workers,size=size,requests=sum(r['requests'] for r in rows),
                        wall_s=sum(r['wall_s'] for r in rows),requests_per_s=sum(r['requests'] for r in rows)/sum(r['wall_s'] for r in rows)))
    floor=[]
    for arm in ARMS[:2]:
        rows=[r for r in read(root/'floor-results.json') if r['arm']==arm]
        floor.append(dict(arm=arm,contexts=len(rows),zero_floor_returned=sum(len(selected[(arm,r['case'])]['hits']) for r in rows),
                          native_default_floor_returned=sum(len(r['hits']) for r in rows)))
    # Relation names contain run identity: emit fixed categories only.
    def storage(path):
        records=read(path);records=records.get('relations',records) if isinstance(records,dict) else records
        result={}
        for category in ('facts','fact_index','fact_fts','fact_vectors','stamps','scale'):
            name=frozen['table_prefix']+category
            row=next((r for r in records if r['relname']==name),None)
            if row:result[category]={k:row[k] for k in ('relation_bytes','total_bytes','indexes_bytes')}
        return result
    resource_rows=lines(root/'resources.jsonl')
    stage_names={'native-negative-controls','native-parser','sqlite-full-and-content-import-replay','native-floor-diagnostic','model-load-authenticate','passage-backfill','query-embedding','deployed-schema-history-index-import','deployed-vector-import','deployed-warm-replay','research-cli-contrasts','scale-import-10000','scale-import-30000'}
    resource_keys={'stage','wall_s','cpu_user_s','cpu_system_s','process_highwater_rss_kib','children_cpu_user_s','children_cpu_system_s','children_highwater_rss_kib'}
    for row in resource_rows:assert row['stage'] in stage_names and set(row)==resource_keys
    summary=dict(status='unlabeled private replay; issue remains open',counts=counts,context_strata=dict(latin=64,cyrillic=63,other=1),
        integrity=dict(returned_hits_checked=invariant_hits,exact_vector_repetitions_checked=exact_checked,source_projection_equal=True,
                       index_counts=index_counts,history_unchanged=True,preexisting_relations_unchanged=True),
        retrieval=retrieval,latency_ms=latency,embedding=embedding_stats,embedding_load_ms=read(root/'embedding-provenance.json')['load_ms'],
        floor_diagnostic=floor,scale=scale_stats,throughput=throughput,resources=resource_rows,
        storage=dict(current=storage(root/'database-current.json'),scale_10000=storage(root/'scale-size-10000.json'),scale_30000=storage(root/'scale-size-30000.json')),
        sqlite_bytes={arm:(root/'sqlite'/(arm+'.db')).stat().st_size for arm in ARMS[:2]},
        backend_memory=read(root/'database-current.json')['backend_memory'],
        unknown=dict(natural_quality=None,actual_agent_outcome=None,hermes_plugin_latency=None,remote_client_latency=None,provider_billing=None,local_operating_cost=None,server_cpu_rss=None,network_bytes=None,ann=None))
    write_new_text(args.output,json.dumps(summary,indent=2)+'\n')
    print(json.dumps(dict(counts=counts,integrity=summary['integrity'])))


if __name__=='__main__':main()
