"""Finite matched-history experiment. No inference, raw-text reads, or cleanup.

Run each declare/run invocation through build-check.sh. All console errors are
fixed categories; details live only in the newly created private packet.
"""
import argparse
import concurrent.futures
import contextlib
import json
import math
import os
from pathlib import Path
import re
import resource
import signal
import subprocess
import sys
import threading
import time
import traceback

from backend import COLUMNS
from deployed import Deployment
from embed import TAG
from matched_query import (sha, safe_read, choose_cases, compile_query, eligible_query,
                           nearest, public_summary, plan_summary)
from outputs import create_output_directory, write_new_text
from source_identity import verify_source

HERE = Path(__file__).absolute().parent
ROOT = HERE.parents[3]
OWN = ROOT / '.research-private/local-plans-scale-01'
HIST = ROOT / '.research-private/phase2-20261106'
OUT = OWN / 'study-02'
FAILURE_OWNED = False
SOURCE_NAMES = ['backend.py','deployed.py','private_replay.py','schema.sql','embed.py',
                'source_identity.py','outputs.py','matched_query.py','matched_study.py','test_matched_query.py']
INPUT_NAMES = {'freeze.json','cases.json','query-vectors.json','sqlite/normalized.json',
               'complete.json','deployment-after.json','embedding-provenance.json'}
NEW_PREFIXES = {10000:'p2_m674s01a_',30000:'p2_m674s01b_'}
SYNTHETIC_PREFIX = 'p2_m674s01t_'
SUFFIXES = ('facts','fact_index','fact_fts','fact_vectors','stamps')


def save(path, value):
    write_new_text(path, json.dumps(value,ensure_ascii=False,sort_keys=True,indent=2)+'\n')


def read(root, name, allowlist):
    return json.loads(safe_read(root,name,allowlist))


def usage():
    r = resource.getrusage(resource.RUSAGE_SELF)
    return dict(user_s=r.ru_utime,system_s=r.ru_stime,process_highwater_rss_kib=r.ru_maxrss)


class Journal:
    def __init__(self,path):
        self.stream = path.open('x'); path.chmod(0o600)
        self.lock = threading.Lock()
    def write(self,value):
        with self.lock:
            self.stream.write(json.dumps(value,ensure_ascii=False)+'\n'); self.stream.flush()
    def close(self):
        self.stream.close()


class Stats:
    """Read-only exact-project container observation; no environment inspection."""
    def __init__(self,out):
        self.out=out; self.phase='initial'; self.process=None; self.thread=None
        self.rows=[]; self.raw=Journal(out/'server-stats.jsonl')
    def start(self):
        argv=['ssh','-oBatchMode=yes','-oConnectTimeout=5','loffe',
              "docker ps --filter label=com.docker.compose.project=memcue-research --format '{{.ID}} {{.Names}}'"]
        try:
            p=subprocess.run(argv,capture_output=True,text=True,timeout=10)
            save(self.out/'stats-discovery.json',dict(argv=argv,exit=p.returncode,stdout=p.stdout,stderr=p.stderr))
            candidates=[line.split() for line in p.stdout.splitlines()]
            if p.returncode or len(candidates)!=1 or len(candidates[0])!=2:
                self.status='unmeasured-exact-container-not-discovered'; return
            ident,name=candidates[0]
            if not re.fullmatch('[a-f0-9]{12,64}',ident) or 'memcue-research' not in name:
                self.status='unmeasured-container-identity-rejected'; return
            self.argv=['ssh','-oBatchMode=yes','-oConnectTimeout=5','loffe',
                       "timeout 900s docker stats --format '{{json .}}' "+ident]
            self.err=(self.out/'stats-stderr.txt').open('x')
            self.process=subprocess.Popen(self.argv,stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,
                                          stderr=self.err,text=True,start_new_session=True)
            def collect():
                for line in self.process.stdout:
                    try:
                        v=json.loads(line[line.find('{'):]); v.update(phase=self.phase,monotonic_s=time.monotonic())
                        self.rows.append(v); self.raw.write(v)
                    except ValueError:
                        pass
            self.thread=threading.Thread(target=collect,daemon=True); self.thread.start()
            self.status='sampling'
        except (OSError,subprocess.SubprocessError):
            self.status='unmeasured-ssh-unavailable'
    def stop(self):
        if self.process:
            if self.process.poll() is None:
                os.killpg(self.process.pid,signal.SIGTERM)
            try:self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid,signal.SIGKILL);self.process.wait(timeout=5)
            self.thread.join(timeout=5);self.process.stdout.close();self.err.close()
            save(self.out/'stats-command.json',dict(argv=self.argv,exit=self.process.returncode,reaped=True))
            self.status='measured-samples' if self.rows else 'unmeasured-no-samples'
        self.raw.close()
        phases={}
        for v in self.rows:
            phase=phases.setdefault(v['phase'],dict(samples=0,cpu_percent_sample_max=0,memory_bytes_sample_max=0))
            phase['samples']+=1
            phase['cpu_percent_sample_max']=max(phase['cpu_percent_sample_max'],float(v['CPUPerc'].rstrip('%')))
            used=v['MemUsage'].split('/')[0].strip()
            m=re.fullmatch(r'([0-9.]+)([kMGT]?i?B)',used)
            if m:
                units={'B':1,'kB':1000,'MB':10**6,'GB':10**9,'KiB':1024,'MiB':2**20,'GiB':2**30}
                phase['memory_bytes_sample_max']=max(phase['memory_bytes_sample_max'],float(m[1])*units.get(m[2],1))
        self.summary=dict(status=self.status,phases=phases,qualification='container sampling maxima, not continuous peaks or backend RSS; container includes other activity')


def inputs():
    hand=read(HIST,'handoff.json',{'handoff.json'})
    if hand['main_packet']!='run-03':raise ValueError('completed packet identity changed')
    packet=HIST/hand['main_packet']
    data={name:read(packet,name,INPUT_NAMES) for name in INPUT_NAMES}
    freeze=data['freeze.json'];after=data['deployment-after.json'];complete=data['complete.json']
    if freeze['table_prefix']!='p2_20261106c_' or not all(complete[x] for x in ('source_unchanged','input_unchanged')):
        raise ValueError('completed packet not verified')
    if not after['history_unchanged'] or not after['existing_relations_unchanged']:
        raise ValueError('historical preservation incomplete')
    if data['embedding-provenance.json']['tag']!=TAG or data['embedding-provenance.json']['provider_calls']!=0:
        raise ValueError('saved vector provenance rejected')
    cases=data['cases.json'];norms={c['id']:c for c in data['sqlite/normalized.json']['cases']}
    vectors=data['query-vectors.json']
    if len(cases)!=128 or set(norms)!=set(vectors) or set(norms)!={c['id'] for c in cases}:
        raise ValueError('cached contexts disagree')
    for vec in vectors.values():
        if len(vec)!=384 or not all(math.isfinite(x) for x in vec) or abs(sum(x*x for x in vec)-1)>1e-5:
            raise ValueError('invalid cached normalized query vector')
    return packet,hand,data,cases,norms,vectors


def declaration():
    global FAILURE_OWNED
    packet,hand,data,cases,norms,vectors=inputs()
    filter_counts={c['id']:sum(len(norms[c['id']]['filter'].get(k) or []) for k in ('Scopes','ScopePrefixes','Origins')) for c in cases}
    selected=choose_cases(cases,filter_counts)
    create_output_directory(OUT)
    FAILURE_OWNED = True
    src=OUT/'source';src.mkdir(mode=0o700)
    hashes={}
    for name in SOURCE_NAMES:
        b=safe_read(HERE,name,set(SOURCE_NAMES));write_new_text(src/name,b.decode());hashes[name]=sha(b)
    identity=verify_source(ROOT)
    inputs_hash={name:sha(safe_read(packet,name,INPUT_NAMES)) for name in sorted(INPUT_NAMES)}
    config=dict(source_packet=str(packet),source_prefix=data['freeze.json']['table_prefix'],new_prefixes=NEW_PREFIXES,
                input_hashes=inputs_hash,source_hashes=hashes,source_identity=identity,
                handoff_sha256=sha(safe_read(HIST,'handoff.json',{'handoff.json'})),
                source_counts=data['deployment-after.json']['counts'],
                selected=[dict(id=c['id'],language=c['language'],filter_shape='scope-filtered' if filter_counts[c['id']] else 'unrestricted') for c in selected],
                argv=sys.orig_argv,declared_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()))
    save(OUT/'private-declaration.json',config)
    public=dict(source_baseline=identity['evaluated_revision'],starting_head=identity['harness_revision'],
                production_digest=identity['production_digest'],source_hashes=hashes,input_hashes=inputs_hash,
                selection='Round robin sorted language/filter-shape strata, each ordered SHA256(saved context ID); no result tuning.',
                contexts=len(selected),all_context_equality=128,languages={k:sum(c['language']==k for c in selected) for k in ('latin','cyrillic','other')},
                filters={k:sum(('scope-filtered' if filter_counts[c['id']] else 'unrestricted')==k for c in selected) for k in ('scope-filtered','unrestricted')},
                schemas='Original completed history tables; new full-history SQL replicas over all primary IDs at 10000 and 30000, including retired and expired latest versions. Same five-table schema and indexes.',
                replication='Stable md5(source ID), ID tie order; cycle all source primary IDs, copy every history version and matching derived rows. New IDs are performance replicas only.',
                variants=['reference','latest-cte'],channels=['lexical','vector'],distance='cosine',model_tag=TAG,
                limit=10,tie_semantics='Unchanged scope/project priority, parsed created timestamp DESC, ID. Latest version selected before all filters.',
                warmup='One untimed request per selected context per arm/channel/scale/concurrency; retained sessions per worker chunk.',
                repetitions=3,concurrency=[1,2],timed_samples_per_group=24,
                quantiles='nearest rank; n=24 p95 rank23, p99/max rank24; small dependent tails',
                budgets=dict(added_history_rows=400000,total_added_relation_bytes=1073741824,client_workers=2,import_writers=1,
                             parallel_workers=0,maintenance_work_mem_mib=64,search_timeout_ms=5000,setup_timeout_ms=180000),
                preflight='Actual source version/derived counts and bytes, conservative scaled 2x source bytes + 128MiB index reserve; check actual totals after each write before proceeding.',
                plans='First two selected contexts, both variants/channels, each scale, ANALYZE BUFFERS JSON; raw private, numeric summaries public.',
                ann=dict(trigger='Any matched 30000 vector group p95 > 100ms, reference or CTE, c1 or c2',threshold_ms=100,
                         max_indexes=1,type='HNSW cosine',m=16,ef_construction=64,ef_search=[40,100],
                         measurements='Same selected contexts, warmup1/repetitions3/c1,c2; unchanged candidate query semantics; require actual index use; no planner forcing.',
                         quality='Filtered exact reference oracle; strict ID recall@10, underfill, eligibility, boundary-valid fraction tolerance3e-6 separately; no natural-task extrapolation'),
                server_metrics='Read-only SSH exact memcue-research project docker stats sampling scoped by phase; unavailable stays unmeasured',
                provider_calls=0,new_embeddings=0,production_changes=False)
    save(OUT/'declaration.json',public)
    variants=[]
    # SQL/parameters are private; freeze before even opening a PG session.
    for scale,prefix in [('current',config['source_prefix']),*[(str(k),v) for k,v in NEW_PREFIXES.items()]]:
        dep=Deployment(data['freeze.json']['endpoint'],prefix)
        for c in selected:
            for arm in ('reference','latest-cte'):
                for channel in ('lexical','vector'):
                    q,p=compile_query(dep,norms[c['id']],c['query'],channel,arm,vectors[c['id']])
                    variants.append(dict(scale=scale,case=c['id'],variant=arm,channel=channel,sql=q,params=p))
    save(OUT/'query-variants.json',variants)
    print('DECLARED: 8 cached contexts; matched current/10000/30000 history; 24 samples per group; conditional ANN.',flush=True)


def connect(dep,connections,setup=False):
    start=time.perf_counter();db=dep.connect();ms=(time.perf_counter()-start)*1000
    db.execute("SET statement_timeout='180s'" if setup else "SET statement_timeout='5s'")
    db.execute("SET maintenance_work_mem='64MB'")
    db.execute("SET lock_timeout='5s'")
    connections.write(dict(prefix=dep.prefix,connection_ms=ms,setup=setup))
    return db


def counts(db,dep):
    out={s:db.execute(f'SELECT count(*) n FROM {dep.table(s)}').fetchone()['n'] for s in SUFFIXES}
    out['primary_ids']=db.execute(f'SELECT count(DISTINCT id) n FROM {dep.table("facts")}').fetchone()['n']
    return out


def size_bytes(db,dep):
    return sum(r['total_bytes'] for r in dep.sizes(db) if r['relkind']=='r' and r['relname'] in {dep.prefix+s for s in SUFFIXES})


def schema_shape(db,dep):
    tables={}
    for suffix in SUFFIXES:
        relation=dep.table(suffix)
        columns=db.execute("SELECT attname,format_type(atttypid,atttypmod) type,attnotnull,attidentity FROM pg_attribute WHERE attrelid=%s::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum",(relation,)).fetchall()
        constraints=db.execute("SELECT contype,pg_get_constraintdef(oid) definition FROM pg_constraint WHERE conrelid=%s::regclass ORDER BY contype,pg_get_constraintdef(oid)",(relation,)).fetchall()
        indexes=db.execute("SELECT pg_get_indexdef(indexrelid) definition FROM pg_index WHERE indrelid=%s::regclass ORDER BY pg_get_indexdef(indexrelid)",(relation,)).fetchall()
        normalize=lambda v: json.loads(json.dumps(v).replace(dep.prefix,'p2_MATCHED_'))
        tables[suffix]=dict(columns=columns,constraints=normalize(constraints),indexes=normalize(indexes))
    return tables


def history_fingerprint(db,dep):
    # SQL-side whole-row hash, never materialize primary content in Python.
    return db.execute(f"SELECT count(*) n,md5(string_agg(md5(row_to_json(f)::text),'' ORDER BY id,version)) digest FROM {dep.table('facts')} f").fetchone()


def synthetic_check(db,endpoint,out):
    dep=Deployment(endpoint,SYNTHETIC_PREFIX);dep.create(db)
    rows=[('retired',1,'active','','global','rule'),('retired',2,'retired','','global','rule'),
          ('expired',1,'active','','global','rule'),('expired',2,'active','2020-01-01','global','rule'),
          ('corrected',1,'active','','global','rule'),('corrected',2,'active','','repo:test','note'),
          ('tie-a',1,'active','','global','rule'),('tie-b',1,'active','','global','rule')]
    with db.transaction():
        for id,ver,status,expires,scope,kind in rows:
            f={k:'' for k in COLUMNS};f.update(id=id,version=ver,status=status,expires=expires,scope=scope,kind=kind,content='synthetic anchor',created='2026-01-01T00:00:00Z',prompt_version=0)
            db.execute(f'INSERT INTO {dep.table("facts")} ({",".join(COLUMNS)}) VALUES ({",".join(["%s"]*len(COLUMNS))})',[f[k] for k in COLUMNS])
    checked=[]
    filters=[dict(Today='2026-11-06'),dict(Today='2026-11-06',Scopes=['global'],Kinds=['rule']),dict(Today='2026-11-06',Scopes=['repo:test'],Kinds=['note'])]
    for f in filters:
        a=[]
        for variant in ('reference','latest-cte'):
            c,j,w,p=eligible_query(dep,dict(filter=f),variant)
            a.append(db.execute(c+f'SELECT f.id,f.version FROM {dep.table("facts")} f'+j+' WHERE '+w+' ORDER BY f.id',p).fetchall())
        assert a[0]==a[1]
        checked.append(a[0])
    assert checked[0]==[dict(id='corrected',version=2),dict(id='tie-a',version=1),dict(id='tie-b',version=1)]
    assert checked[1]==[dict(id='tie-a',version=1),dict(id='tie-b',version=1)]
    assert checked[2]==[dict(id='corrected',version=2)]
    # Append a retirement without touching any derived-index invariant.
    db.execute(f"INSERT INTO {dep.table('facts')} SELECT id,3,content,origin,created,'retired',author,expires,kind,scope,source,evidence,review,prompt_version,channel FROM {dep.table('facts')} WHERE id='corrected' AND version=2")
    for variant in ('reference','latest-cte'):
        c,j,w,p=eligible_query(dep,dict(filter=filters[2]),variant)
        assert db.execute(c+f'SELECT f.id,f.version FROM {dep.table("facts")} f'+j+' WHERE '+w,p).fetchall()==[]
    save(out/'synthetic-db.json',dict(passed=True,rows_readback=counts(db,dep),checks=8))
    return dep


def mapping(source,n):
    return f'''WITH ids AS (SELECT id,row_number() OVER(ORDER BY md5(id),id) rn FROM {source.table('facts')} GROUP BY id),
      mapping AS (SELECT 'replica_'||lpad(g::text,8,'0') id,ids.id source_id FROM generate_series(1,{n}::integer) g
      JOIN ids ON ids.rn=((g-1)%(SELECT count(*) FROM ids))+1) '''


def build_scale(db,source,dep,n,out):
    dep.create(db)
    m=mapping(source,n)
    with db.transaction():
        cols=','.join('m.id' if c=='id' else 'f.'+c for c in COLUMNS)
        db.execute(m+f'INSERT INTO {dep.table("facts")} SELECT {cols} FROM mapping m JOIN {source.table("facts")} f ON f.id=m.source_id')
        db.execute(m+f'INSERT INTO {dep.table("fact_index")} (id,version,stems,numbers) SELECT m.id,i.version,i.stems,i.numbers FROM mapping m JOIN {source.table("fact_index")} i ON i.id=m.source_id ORDER BY m.id')
        db.execute(m+f'INSERT INTO {dep.table("fact_fts")} SELECT ni.rid,x.lexemes FROM mapping m JOIN {source.table("fact_index")} oi ON oi.id=m.source_id JOIN {source.table("fact_fts")} x ON x.rid=oi.rid JOIN {dep.table("fact_index")} ni ON ni.id=m.id')
        db.execute(m+f'INSERT INTO {dep.table("fact_vectors")} SELECT m.id,v.tag,v.dim,v.text_hash,v.version,v.vec FROM mapping m JOIN {source.table("fact_vectors")} v ON v.id=m.source_id')
    for suffix in SUFFIXES:db.execute('ANALYZE '+dep.table(suffix))
    actual=counts(db,dep)
    assert actual['primary_ids']==n
    # Verify every written history and derived value using SQL EXCEPT ALL, both directions.
    checks={}
    for suffix in ('facts','fact_index','fact_fts','fact_vectors'):
        if suffix=='facts':
            expected=f'SELECT {cols} FROM mapping m JOIN {source.table("facts")} f ON f.id=m.source_id'
            written=f'SELECT * FROM {dep.table(suffix)}'
        elif suffix=='fact_index':
            expected=f'SELECT m.id,i.version,i.stems,i.numbers FROM mapping m JOIN {source.table(suffix)} i ON i.id=m.source_id'
            written=f'SELECT id,version,stems,numbers FROM {dep.table(suffix)}'
        elif suffix=='fact_vectors':
            expected=f'SELECT m.id,v.tag,v.dim,v.text_hash,v.version,v.vec::text FROM mapping m JOIN {source.table(suffix)} v ON v.id=m.source_id'
            written=f'SELECT id,tag,dim,text_hash,version,vec::text FROM {dep.table(suffix)}'
        else:
            expected=f'SELECT m.id,x.lexemes FROM mapping m JOIN {source.table("fact_index")} i ON i.id=m.source_id JOIN {source.table(suffix)} x ON x.rid=i.rid'
            written=f'SELECT i.id,x.lexemes FROM {dep.table("fact_index")} i JOIN {dep.table(suffix)} x ON x.rid=i.rid'
        checks[suffix]=db.execute(m+'SELECT count(*) n FROM (('+expected+' EXCEPT ALL '+written+') UNION ALL ('+written+' EXCEPT ALL '+expected+')) diffs').fetchone()['n']
    assert all(v==0 for v in checks.values())
    save(out/f'load-{n}.json',dict(counts=actual,exact_sql_readback_differences=checks,sizes=dep.sizes(db),fingerprint=history_fingerprint(db,dep)))
    return actual


def run_query(db,dep,c,norms,vectors,channel,variant):
    if channel=='lexical' and not norms[c['id']]['pg_query']:
        return [],0.0
    q,p=compile_query(dep,norms[c['id']],c['query'],channel,variant,vectors[c['id']])
    t=time.perf_counter();hits=db.execute(q,p).fetchall()
    return hits,(time.perf_counter()-t)*1000


def eligible_ids(db,dep,norm,variant):
    c,j,w,p=eligible_query(dep,norm,variant)
    # AllTerms is a channel eligibility rule; include it in this exhaustive check.
    if norm['filter'].get('AllTerms'):
        j+=f' JOIN {dep.table("fact_index")} i ON (i.id,i.version)=(f.id,f.version) JOIN {dep.table("fact_fts")} x ON x.rid=i.rid '
        w+=" AND x.lexemes @@ to_tsquery('simple',%s)";p.append(norm['pg_query'])
    return db.execute(c+f'SELECT f.id,f.version FROM {dep.table("facts")} f'+j+' WHERE '+w+' ORDER BY f.id',p).fetchall()


def equal_checks(db,dep,cases,norms,vectors,out,label,unchanged_reference=False):
    raw=Journal(out/f'equality-{label}.jsonl');n=0;eligible_total=0
    try:
        for c in cases:
            eligible=[eligible_ids(db,dep,norms[c['id']],v) for v in ('reference','latest-cte')]
            assert eligible[0]==eligible[1], 'eligible identity mismatch'
            eligible_total+=len(eligible[0])
            raw.write(dict(case=c['id'],type='eligible',rows=eligible[0],equal=True))
            for channel in ('lexical','vector'):
                a,ms=run_query(db,dep,c,norms,vectors,channel,'reference')
                b,ms=run_query(db,dep,c,norms,vectors,channel,'latest-cte')
                assert a==b,'rank/score mismatch'
                if unchanged_reference:
                    r=dep.search(db,norms[c['id']],c['query'],'pg-lexical' if channel=='lexical' else 'pg-vector-exact',vectors[c['id']])['hits']
                    assert a==r,'reference SQL drift'
                raw.write(dict(case=c['id'],type=channel,hits=a,equal=True,unchanged_reference=unchanged_reference));n+=1
    finally:raw.close()
    return dict(contexts=len(cases),ranking_pairs=n,eligible_rows_compared=eligible_total,
                exact_eligible_ids_versions=True,exact_rank_scores_ties=True,unchanged_reference_checked=unchanged_reference)


def benchmark(dep,cases,norms,vectors,label,connections,raw,workloads,variant='both',channel_only=None):
    rows=[]
    for workers in (1,2):
        for arm in (('reference','latest-cte') if variant=='both' else (variant,)):
            for channel in (('lexical','vector') if not channel_only else (channel_only,)):
                start=time.monotonic()
                def worker(group):
                    results=[]
                    with connect(dep,connections) as db:
                        if arm.startswith('ann-'):
                            db.execute('SET hnsw.ef_search='+arm.split('-')[1]);query_arm='latest-cte'
                        else:query_arm=arm
                        for c in group:run_query(db,dep,c,norms,vectors,channel,query_arm)
                        for rep in range(3):
                            for c in group:
                                hits,ms=run_query(db,dep,c,norms,vectors,channel,query_arm)
                                results.append(dict(scale=label,variant=arm,channel=channel,concurrency=workers,rep=rep,case=c['id'],sql_ms=ms,hits=hits))
                    return results
                with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
                    for results in pool.map(worker,[cases[i::workers] for i in range(workers)]):
                        for row in results:raw.write(row);rows.append(row)
                workloads.write(dict(scale=label,variant=arm,channel=channel,concurrency=workers,requests=24,warmup_requests=8,wall_s=time.monotonic()-start))
    return rows


def explain_subset(db,dep,cases,norms,vectors,label,out,variant_list=('reference','latest-cte'),channels=('lexical','vector')):
    summaries=[];raw=[]
    for ordinal,c in enumerate(cases[:2]):
        for variant in variant_list:
            for channel in channels:
                if channel=='lexical' and not norms[c['id']]['pg_query']:continue
                arm=variant
                if variant.startswith('ann-'):
                    db.execute('SET hnsw.ef_search='+variant.split('-')[1]);arm='latest-cte'
                q,p=compile_query(dep,norms[c['id']],c['query'],channel,arm,vectors[c['id']])
                t=time.perf_counter();plan=db.execute('EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+q,p).fetchone()['QUERY PLAN'][0]
                ms=(time.perf_counter()-t)*1000
                raw.append(dict(case=c['id'],variant=variant,channel=channel,sql=q,params=p,plan=plan,client_ms=ms))
                summaries.append(dict(scale=label,context_ordinal=ordinal,variant=variant,channel=channel,explain_client_ms=ms,**plan_summary(plan)))
    save(out/f'plans-{label}.json',raw)
    return summaries,raw


def ann_quality(db,dep,cases,norms,vectors,exact,measured,out):
    eligible={c['id']:{(r['id'],r['version']) for r in eligible_ids(db,dep,norms[c['id']],'reference')} for c in cases}
    oracle={r['case']:r['hits'] for r in exact if r['variant']=='reference' and r['channel']=='vector' and r['concurrency']==1 and r['rep']==0}
    # Recompute exact distances for returned IDs; ANN score already is actual cosine.
    records=[]
    for r in measured:
        truth=oracle[r['case']];hits=r['hits'];k=len(truth)
        ids={h['id'] for h in truth};boundary=1-truth[-1]['score'] if truth else None
        valid=sum((h['id'],h['version']) in eligible[r['case']] for h in hits)
        records.append(dict(case=r['case'],variant=r['variant'],concurrency=r['concurrency'],rep=r['rep'],
                            exact_count=k,returned=len(hits),strict_id_recall_at10=len(ids & {h['id'] for h in hits})/k if k else None,
                            underfill=max(0,k-len(hits)),eligible=valid==len(hits),
                            within_exact_kth_boundary_fraction=sum(1-h['score']<=boundary+3e-6 for h in hits)/len(hits) if hits and boundary is not None else None))
    save(out/'ann-quality.json',records)
    result=[]
    for variant in ('ann-40','ann-100'):
        for conc in (1,2):
            group=[r for r in records if r['variant']==variant and r['concurrency']==conc]
            result.append(dict(variant=variant,concurrency=conc,samples=len(group),
                               strict_id_recall_at10_mean=sum(r['strict_id_recall_at10'] for r in group if r['strict_id_recall_at10'] is not None)/len(group),
                               underfilled_requests=sum(r['underfill']>0 for r in group),all_eligible=all(r['eligible'] for r in group),
                               boundary_valid_fraction_mean=sum(r['within_exact_kth_boundary_fraction'] or 0 for r in group)/len(group)))
    return result


def execute():
    global FAILURE_OWNED
    start=time.monotonic();before_cpu=usage()
    cfg=read(OUT,'private-declaration.json',{'private-declaration.json'})
    public=read(OUT,'declaration.json',{'declaration.json'})
    if (OUT/'run-start.json').exists():raise FileExistsError('run already attempted; preserve packet')
    save(OUT/'run-start.json',dict(argv=sys.orig_argv,started_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
    FAILURE_OWNED = True
    packet,hand,data,cases,norms,vectors=inputs()
    for name,expected in cfg['input_hashes'].items():assert sha(safe_read(packet,name,INPUT_NAMES))==expected
    for name,expected in cfg['source_hashes'].items():assert sha(safe_read(HERE,name,set(SOURCE_NAMES)))==expected
    endpoint=read(HIST,'endpoint.json',{'endpoint.json'})
    # Credential file is passed to libpq only, never opened/hashed/copied by Python.
    passfile=ROOT/'.research-private/loffe-research.pgpass'
    for component in (passfile,*passfile.parents):
        if component.is_symlink():raise ValueError('credential alias forbidden')
    if passfile.stat().st_mode & 0o777 != 0o600:raise ValueError('private passfile mode required')
    os.environ['PGPASSFILE']=str(passfile)
    source=Deployment(endpoint,cfg['source_prefix']);selected=[next(c for c in cases if c['id']==x['id']) for x in cfg['selected']]
    connections=Journal(OUT/'connections.jsonl');raw=Journal(OUT/'timings.jsonl');workloads=Journal(OUT/'workloads.jsonl');resources=Journal(OUT/'resources.jsonl')
    stats=Stats(OUT);stats.start();allrows=[];plans=[];equality={};actual={};deps=[];synthetic=None
    @contextlib.contextmanager
    def phase(name):
        stats.phase=name;t=time.monotonic();u=usage()
        try:yield
        finally:
            v=usage();resources.write(dict(phase=name,wall_s=time.monotonic()-t,user_s=v['user_s']-u['user_s'],system_s=v['system_s']-u['system_s'],process_highwater_rss_kib=v['process_highwater_rss_kib']))
    try:
        with connect(source,connections,True) as db:
            # Source row reads restricted to the one verified completed namespace.
            with phase('preflight'):
                actual['current']=counts(db,source)
                assert actual['current']['facts']==cfg['source_counts']['facts'] and actual['current']['primary_ids']==7713
                assert all(actual['current'][s]==cfg['source_counts'][s] for s in SUFFIXES)
                original_fingerprint=history_fingerprint(db,source)
                original_metadata=source.preflight(db)
                original_schema=schema_shape(db,source)
                save(OUT/'source-schema.json',original_schema)
                for prefix in [SYNTHETIC_PREFIX,*NEW_PREFIXES.values()]:Deployment(endpoint,prefix).preflight(db,True)
                source_bytes=size_bytes(db,source)
                expected_history=sum(db.execute(mapping(source,n)+f'SELECT count(*) n FROM mapping m JOIN {source.table("facts")} f ON f.id=m.source_id').fetchone()['n'] for n in NEW_PREFIXES)
                expected_bytes=math.ceil(source_bytes*40000/7713*2)+128*2**20
                save(OUT/'preflight.json',dict(actual=actual,expected_added_history_rows=expected_history+9,expected_added_bytes_upper=expected_bytes,source_sizes=source.sizes(db),source_fingerprint=original_fingerprint,metadata=original_metadata))
                assert expected_history+9<=400000 and expected_bytes<=2**30,'preflight resource budget'
            with phase('synthetic-db-equality-first'):
                synthetic=synthetic_check(db,endpoint,OUT)
            with phase('current-all128-equality'):
                db.execute("SET statement_timeout='5s'")
                equality['current']=equal_checks(db,source,cases,norms,vectors,OUT,'current',True)
                eligible_counts=[dict(language=c['language'],filter_shape=next(s['filter_shape'] for s in cfg['selected'] if s['id']==c['id']),eligible_count=len(eligible_ids(db,source,norms[c['id']],'reference'))) for c in selected]
                save(OUT/'selected-strata-counts.json',eligible_counts)
            with phase('current-timing'):
                allrows+=benchmark(source,selected,norms,vectors,'current',connections,raw,workloads)
                summary,_=explain_subset(db,source,selected,norms,vectors,'current',OUT);plans+=summary
        print('PASSED: synthetic lifecycle and all 128 saved contexts; exact eligibility/ranks/scores match reference.',flush=True)
        for n,prefix in NEW_PREFIXES.items():
            dep=Deployment(endpoint,prefix);deps.append(dep)
            with phase('matched-import-'+str(n)):
                with connect(dep,connections,True) as db:
                    actual[str(n)]=build_scale(db,source,dep,n,OUT)
                    clone_schema=schema_shape(db,dep)
                    save(OUT/f'schema-{n}.json',clone_schema)
                    assert clone_schema==original_schema,'matched schema/index mismatch'
                    assert sum(actual[k]['facts'] for k in actual if k!='current')+9<=400000
                    assert sum(size_bytes(db,d) for d in [synthetic,*deps])<=2**30
            with phase('matched-equality-'+str(n)):
                with connect(dep,connections) as db:
                    equality[str(n)]=equal_checks(db,dep,selected,norms,vectors,OUT,str(n),True)
            with phase('matched-timing-'+str(n)):
                allrows+=benchmark(dep,selected,norms,vectors,str(n),connections,raw,workloads)
                with connect(dep,connections) as db:
                    summary,_=explain_subset(db,dep,selected,norms,vectors,str(n),OUT);plans+=summary
            print('MEASURED: matched history at '+str(n)+' primary IDs; SQL readback and query equality passed.',flush=True)
        groups=public_summary(allrows)
        trigger=any(r['scale']=='30000' and r['channel']=='vector' and r['p95_ms']>100 for r in groups)
        ann=dict(status='NOT_NEEDED_FOR_THIS_DECLARATION',triggered=trigger)
        if trigger:
            dep=deps[-1]
            with phase('ann-index-build'):
                with connect(dep,connections,True) as db:
                    name=dep.prefix+'hnsw_cosine'
                    t=time.perf_counter();db.execute(f'CREATE INDEX "{name}" ON {dep.table("fact_vectors")} USING hnsw (vec public.vector_cosine_ops) WITH (m=16,ef_construction=64)')
                    build_s=time.perf_counter()-t
                    readback=db.execute("SELECT i.relname,pg_relation_size(i.oid) bytes,pg_get_indexdef(i.oid) definition,pg_get_userbyid(i.relowner) owner FROM pg_class i JOIN pg_namespace n ON n.oid=i.relnamespace WHERE n.nspname='research' AND i.relname=%s",(name,)).fetchone()
                    assert readback and readback['owner']==endpoint['user']
                    assert sum(size_bytes(db,d) for d in [synthetic,*deps])<=2**30
                    save(OUT/'ann-build.json',dict(wall_s=build_s,index=readback))
            with phase('ann-timing'):
                annrows=[]
                for ef in (40,100):annrows+=benchmark(dep,selected,norms,vectors,'30000',connections,raw,workloads,'ann-'+str(ef),'vector')
                with connect(dep,connections) as db:
                    ap,arp=explain_subset(db,dep,selected,norms,vectors,'ann',OUT,('ann-40','ann-100'),('vector',));plans+=ap
                    def used(node):return node.get('Index Name')==name or any(used(n) for n in node.get('Plans',[]))
                    usage_flags=[used(p['plan']['Plan']) for p in arp]
                    quality=ann_quality(db,dep,selected,norms,vectors,[r for r in allrows if r['scale']=='30000'],annrows,OUT)
                ann=dict(status='ANN_MEASURED' if all(usage_flags) else 'INDEX_NOT_USED_FOR_ALL_PROBED_PLANS',triggered=True,
                         probed_plans=len(usage_flags),index_used_plans=sum(usage_flags),build_s=build_s,index_bytes=readback['bytes'],quality=quality,
                         qualification='Latency under HNSW settings only counts as ANN where actual plan uses HNSW. Tied replicas make ID recall and kth-boundary validity different.')
                allrows+=annrows
        with phase('final-readback'):
            with connect(source,connections,True) as db:
                assert history_fingerprint(db,source)==original_fingerprint
                after=source.preflight(db)
                assert all(r in after['existing_relations'] for r in original_metadata['existing_relations'])
                final={}
                for dep in [synthetic,*deps]:
                    final[dep.prefix]=dict(counts=counts(db,dep),sizes=dep.sizes(db),fingerprint=history_fingerprint(db,dep))
                added_bytes=sum(size_bytes(db,d) for d in [synthetic,*deps]);assert added_bytes<=2**30
                assert all(r['owner']==endpoint['user'] for v in final.values() for r in v['sizes'])
                save(OUT/'final-readback.json',dict(targets=final,total_added_bytes=added_bytes,source_history_equal=True,preexisting_relation_identity_equal=True))
        stats.stop()
        after_cpu=usage()
        result=dict(status='measured',declaration_sha256=sha((OUT/'declaration.json').read_bytes()),
                    counts=actual,equality=equality,selected_strata=eligible_counts,timings=public_summary(allrows),plans=plans,ann=ann,
                    added_history_rows=sum(v['facts'] for k,v in actual.items() if k!='current')+9,added_relation_bytes=added_bytes,
                    driver=dict(wall_s=time.monotonic()-start,user_s=after_cpu['user_s']-before_cpu['user_s'],system_s=after_cpu['system_s']-before_cpu['system_s'],process_highwater_rss_kib=after_cpu['process_highwater_rss_kib'],qualification='Linux cumulative process high-water RSS, includes Python cached normalization/JSON and libpq, excludes PG server'),
                    server=stats.summary,source_preserved=True,provider_calls=0,new_embeddings=0,
                    unknowns=['natural relevance/agent outcomes','remote-client network bytes/latency','monthly cost','production protocol/writes/recovery','independent acceptance and CI'])
        save(OUT/'aggregates.json',result)
        # Original contexts/cache/source remain byte-identical after all SQL work.
        for name,h in cfg['input_hashes'].items():assert sha(safe_read(packet,name,INPUT_NAMES))==h
        for name,h in cfg['source_hashes'].items():assert sha(safe_read(HERE,name,set(SOURCE_NAMES)))==h
        assert verify_source(ROOT)['production_digest']==cfg['source_identity']['production_digest']
        save(OUT/'complete.json',dict(status='complete',wall_s=time.monotonic()-start,inputs_preserved=True,source_preserved=True,exit=0))
        print('COMPLETE: raw records/private plans saved; ANN '+ann['status']+'; no production change.',flush=True)
    finally:
        if not hasattr(stats,'summary'):stats.stop()
        for journal in (connections,raw,workloads,resources):journal.close()


def main():
    os.umask(0o077)
    parser=argparse.ArgumentParser();parser.add_argument('command',choices=['declare','run']);args=parser.parse_args()
    try:
        if args.command=='declare':declaration()
        else:execute()
    except BaseException:
        if FAILURE_OWNED:
            try:write_new_text(OUT/('failure-'+args.command+'.txt'),traceback.format_exc())
            except FileExistsError:pass
        print('FAILED: private failure record retained; no cleanup or retry into existing targets.',file=sys.stderr)
        return 1
    return 0

if __name__=='__main__':sys.exit(main())
