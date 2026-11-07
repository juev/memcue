"""Single finite read-only #674 invocation; saved packets are never replaced.

Explicit commands: declare, metadata, test-red, test-green, run. The GREEN
source supplement must be frozen before test-green. No code imports an embedder.
All private reads pass exact roots, link checks, explicit names and seals.
"""
import concurrent.futures
import contextlib
import io
import json
import os
from pathlib import Path
import resource
import subprocess
import sys
import threading
import time
import traceback
import unittest

import psycopg
from psycopg.rows import dict_row
from ann_shape import (Operations, agreement, candidate, plan_projection, reference,
                       save, validate_vector)
from deployed import Deployment
from embed import TAG, literal
from matched_query import nearest, safe_read, sha
from outputs import create_output_directory, write_new_text

HERE=Path(__file__).absolute().parent
ROOT=HERE.parents[3]
OWN=ROOT/'.research-private/local-ann-shape-01'
OLD=ROOT/'.research-private/local-plans-scale-01'
CACHE=ROOT/'.research-private/phase2-20261106/run-03'
PUBLIC=HERE/'ann_shape-declaration.json'
SOURCES=('ann_shape.py','ann_shape_study.py','test_ann_shape.py','test_ann_shape_offline.py')
DEPENDENCIES=('backend.py','deployed.py','embed.py','matched_query.py','outputs.py')
PREFIXES={'current':'p2_20261106c_','10000':'p2_m674s01a_','30000':'p2_m674s01b_'}
INDEX='p2_m674s01b_hnsw_cosine'
LIMITS={'metadata':(2,10),'test-red':(12,20),'test-green':(12,20),'run':(501,250)}
# 527 maximum issued SELECT/EXPLAIN: 3 metadata, 4 diagnostic, 24 tests,
# 288 correctness, 16 all-context plan coverage, 48 warmup, 144 timed.


def read(path,names):
    return json.loads(safe_read(path.parent,path.name,names))


def inputs():
    manifest=read(OLD/'allowlisted-manifest.json',{'allowlisted-manifest.json'})
    assert len(manifest['files'])==102
    allow={r['path']:r for r in manifest['files']}
    used={}
    def old(name):
        b=safe_read(OLD,name,allow)
        assert sha(b)==allow[name]['sha256'] and len(b)==allow[name]['bytes']
        used[name]=sha(b)
        return json.loads(b)
    cfg=old('study-02/private-declaration.json')
    assert Path(cfg['source_packet'])==CACHE and cfg['source_prefix']==PREFIXES['current']
    assert old('study-02/complete.json')['exit']==0
    oldfinal=old('study-02/final-readback.json')
    oldpre=old('study-02/preflight.json')
    old('study-02/ann-build.json')
    old('study-02/source-schema.json')
    oldplans=old('study-02/plans-ann.json')
    data={}
    for name,expected in cfg['input_hashes'].items():
        assert name in {'freeze.json','cases.json','query-vectors.json','sqlite/normalized.json','complete.json','deployment-after.json','embedding-provenance.json'}
        b=safe_read(CACHE,name,cfg['input_hashes']);assert sha(b)==expected
        data[name]=json.loads(b)
    assert data['freeze.json']['table_prefix']==PREFIXES['current']
    assert data['embedding-provenance.json']['tag']==TAG
    cases=data['cases.json'];norms={c['id']:c for c in data['sqlite/normalized.json']['cases']}
    vectors=data['query-vectors.json']
    assert len(cases)==128 and set(norms)==set(vectors)=={c['id'] for c in cases}
    for v in vectors.values():validate_vector(v)
    selected=[next(c for c in cases if c['id']==s['id']) for s in cfg['selected']]
    assert len(selected)==8 and not norms[selected[4]['id']]['pg_query']
    oracles={};eligible={}
    for scale in PREFIXES:
        name='study-02/equality-'+scale+'.jsonl'
        b=safe_read(OLD,name,allow);assert sha(b)==allow[name]['sha256'];used[name]=sha(b)
        rows=[json.loads(x) for x in b.decode().splitlines()]
        oracles[scale]={r['case']:r['hits'] for r in rows if r['type']=='vector'}
        eligible[scale]={r['case']:{(x['id'],x['version']) for x in r['rows']} for r in rows if r['type']=='eligible'}
    acceptance=read(ROOT/'.research-private/parent-matched-report-acceptance-01/acceptance.json',{'acceptance.json'})
    assert acceptance['passed']
    return dict(cfg=cfg,data=data,cases=cases,norms=norms,vectors=vectors,selected=selected,
                oracles=oracles,eligible=eligible,oldfinal=oldfinal,oldpre=oldpre,oldplans=oldplans,
                endpoint=data['freeze.json']['endpoint'],used=used,
                seal_sha256=sha(safe_read(OLD,'allowlisted-manifest.json',{'allowlisted-manifest.json'})))


def identities():
    return {n:sha(safe_read(HERE,n,set(SOURCES+DEPENDENCIES))) for n in SOURCES+DEPENDENCIES}


def freeze(name):
    out=create_output_directory(OWN/name)
    hashes=identities()
    for n in SOURCES+DEPENDENCIES:
        write_new_text(out/n,safe_read(HERE,n,set(SOURCES+DEPENDENCIES)).decode())
    return hashes


def declare():
    d=inputs()
    assert subprocess.check_output(['git','branch','--show-current'],text=True).strip()=='hermes/memcue-postgres-674'
    assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()=='d14af092d6271c807a7f8ff83a75d8a4bcda0850'
    assert not subprocess.check_output(['git','diff','--name-only','HEAD'],text=True)
    hashes=freeze('source-red')
    plans=[]
    for s,prefix in PREFIXES.items():
        dep=Deployment(d['endpoint'],prefix)
        for c in d['cases'] if s=='current' else d['selected']:
            n=d['norms'][c['id']];v=d['vectors'][c['id']]
            plans.append(dict(scale=s,case=c['id'],reference=reference(dep,n,c['query'],v),candidate=candidate(dep,n,c['query'],v)))
    declaration=dict(starting_head='d14af092d6271c807a7f8ff83a75d8a4bcda0850',
        production_baseline='a5de0b989ceb1046f4933b19cae7ef60bed0ede2',model_tag=TAG,
        study='vector-query-shape',provider_calls=0,new_embeddings=0,
        causes_ranked=[
            'Multi-key ORDER BY prevents a distance-only index path from satisfying the complete order; compare raw KNN with added ID key.',
            'Eligibility joins/global latest aggregation favor join-first sorting; compare a correlated eligible EXISTS before distance-only limit.',
            'Expression/operator/opclass or index invalidity mismatch; inspect actual catalog and raw distance KNN.',
            'Cost/selectivity choice rather than capability; compare unforced raw and eligible KNN, no forced speedup arm.',
            'Prepared generic plans or partial-index implication; inspect actual full-index predicate and disable auto-prepare for every experimental query.'],
        candidate='One distance-only inner vector scan with correlated eligibility EXISTS OFFSET 0; FETCH WITH TIES; outer original parsed-timestamp/scope/ID tie order and limit10.',
        tdd='Red source deliberately cuts inner ties with ONLY; only prospective semantic repair replaces ONLY with WITH TIES. Same six live tests and fixtures then green. No new case selection.',
        selection='Reuse exactly the saved fixed eight contexts in saved order; ordinal4 has empty lexical query but vector executes.',
        correctness=dict(current=128,replica10000=8,replica30000=8,sql_per_context=2,
                         oracle='Live exact reference plus candidate on separate indexscan/bitmapscan-disabled correctness connections, also check saved exact ranks and eligible membership.',tolerance=3e-6),
        diagnostic_plans=['first-saved-context reference','first-saved-vector raw distance KNN','same raw KNN with ID tie','first-saved-context candidate'],
        coverage='After equality: all eight selected contexts at both ef_search settings, unprepared default planner; every timing query is unprepared with identical options/templates/parameters. Sixteen plans do not certify every timing execution.',
        preparation='psycopg prepare_threshold=None and prepare=False. No SQL PREPARE or forced generic/custom execution; generic behavior remains unmeasured. Full index predicate inspected.',
        timing=dict(scale=30000,arms=['reference','ann-40','ann-100'],contexts=8,repetitions=3,concurrency=[1,2],warmup_per_context_per_group=1,timed_samples=144,warmups=48,cohorts=1,
                    reference='Query bytes and default planner settings unchanged; only read-only/session resource options shared with candidate.',
                    stop='Any correctness mismatch: no further plans/timings. Any coverage plan without HNSW: INDEX_NOT_USED; no timing cohort.'),
        budgets=dict(sql_total=527,hard_sql_total=600,metadata_initial=2,metadata_final=1,diagnostic_explain=4,coverage_explain=16,synthetic_sql_red=12,synthetic_sql_green=12,
                     correctness_sql=288,timing_sql=192,driver_wall_s=300,stage_wall_s={k:v[1] for k,v in LIMITS.items()},query_timeout_s=5,clients=2,parallel_workers=0,work_mem_mib=64,maintenance_work_mem_mib=64),
        output='Public fixed categories and numeric aggregates only; all SQL, values, identities, paths, raw plans private.',
        resources='Client wall/user/system/high-water RSS only; PG server resources unknown; collector not repaired.',
        source_hashes=hashes,limitations=['Reused development queries, not held-out relevance.','ANN cannot guarantee all equal-distance identities even with FETCH WITH TIES.','24 dependent timed samples/group; nearest-rank p95 rank23 and p99/max rank24.','No model/provider/agent calls; production unchanged.'])
    save(PUBLIC,declaration)
    save(OWN/'private-declaration.json',dict(public_sha256=sha(PUBLIC.read_bytes()),source_hashes=hashes,
         inputs=d['used'],cache_hashes=d['cfg']['input_hashes'],seal_sha256=d['seal_sha256'],
         cli_thread_id=os.environ.get('CODEX_THREAD_ID'),argv=sys.orig_argv,selected=d['cfg']['selected'],
         source_packet=str(CACHE),query_templates=plans,created_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
    print('DECLARED: maximum527 read-only SQL;300s aggregate driver caps; one candidate; no forcing.')


def connect(d,out,*,exact=False,ef=40):
    credential=ROOT/'.research-private/loffe-research.pgpass'
    for p in (credential,*credential.parents):
        if p.is_symlink():raise ValueError('credential alias forbidden')
    assert credential.stat().st_mode&0o777==0o600
    os.environ['PGPASSFILE']=str(credential) # libpq only; never open/hash password bytes.
    options=('-c default_transaction_read_only=on -c statement_timeout=5000 -c lock_timeout=5000 '
             '-c max_parallel_workers_per_gather=0 -c jit=off -c work_mem=64MB '
             '-c maintenance_work_mem=64MB -c hnsw.ef_search='+str(ef))
    if exact:options+=' -c enable_indexscan=off -c enable_bitmapscan=off'
    start=time.monotonic()
    db=psycopg.connect(**d['endpoint'],options=options,connect_timeout=5,autocommit=True,row_factory=dict_row,prepare_threshold=None)
    with out['connection_lock']:
        out['connections'].append(dict(connection_ms=(time.monotonic()-start)*1000,exact=exact,ef=ef,options=options,prepare_threshold=db.prepare_threshold))
    return db


def fingerprints_sql(d):
    parts=[]
    for scale,prefix in PREFIXES.items():
        dep=Deployment(d['endpoint'],prefix)
        values=[]
        for suffix in ('facts','fact_index','fact_fts','fact_vectors','stamps'):
            order='id,version' if suffix=='facts' else 'id,tag' if suffix=='fact_vectors' else 'id' if suffix=='fact_index' else 'rid' if suffix=='fact_fts' else 'name'
            values.append("'"+suffix+"',(SELECT jsonb_build_object('n',count(*),'digest',md5(string_agg(md5(row_to_json(f)::text),'' ORDER BY "+order+'))) FROM '+dep.table(suffix)+' f)')
        values.append("'primary_ids',(SELECT count(DISTINCT id) FROM "+dep.table('facts')+')')
        parts.append("'"+scale+"',jsonb_build_object("+','.join(values)+')')
    return 'SELECT jsonb_build_object('+','.join(parts)+') snapshot'


def stage(stage):
    d=inputs();seconds=LIMITS[stage][1];path=create_output_directory(OWN/stage)
    out=dict(connections=[],connection_lock=threading.Lock(),status='INCOMPLETE')
    op=Operations(path/'operations.jsonl',LIMITS[stage][0],seconds)
    before=resource.getrusage(resource.RUSAGE_SELF);start=time.monotonic();code=1
    save(path/'start.json',dict(argv=sys.orig_argv,source_hashes=identities(),monotonic_s=start,budget_s=seconds))
    try:
        if stage=='metadata':
            with connect(d,out) as db:
                q="""SELECT current_setting('server_version') server_version,current_setting('server_version_num') server_version_num,
                current_user,current_database(),current_setting('default_transaction_read_only') read_only,
                current_setting('max_parallel_workers_per_gather') parallel_workers,current_setting('work_mem') work_mem,
                current_setting('maintenance_work_mem') maintenance_work_mem,current_setting('statement_timeout') statement_timeout,
                (SELECT extversion FROM pg_extension WHERE extname='vector') vector_version,
                (SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='vector') vector_namespace,
                (SELECT jsonb_agg(jsonb_build_object('name',i.relname,'owner',pg_get_userbyid(i.relowner),
                   'valid',ix.indisvalid,'ready',ix.indisready,'predicate',pg_get_expr(ix.indpred,ix.indrelid),
                   'definition',pg_get_indexdef(i.oid),'bytes',pg_relation_size(i.oid)))
                 FROM pg_index ix JOIN pg_class i ON i.oid=ix.indexrelid JOIN pg_class t ON t.oid=ix.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace
                 WHERE n.nspname='research' AND t.relname='p2_m674s01b_fact_vectors') indexes,
                (SELECT jsonb_agg(jsonb_build_object('table',c.relname,'column',a.attname,'type',format_type(a.atttypid,a.atttypmod),'not_null',a.attnotnull))
                 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
                 WHERE n.nspname='research' AND c.relname=ANY(%s) AND a.attnum>0 AND NOT a.attisdropped) columns"""
                names=[PREFIXES['30000']+s for s in ('facts','fact_index','fact_fts','fact_vectors')]
                rows,_=op.execute(db,q,[names],phase='metadata',label=0);meta=rows[0]
                assert meta['read_only']=='on' and meta['vector_namespace']=='public'
                index=next(x for x in meta['indexes'] if x['name']==INDEX)
                assert index['owner']==d['endpoint']['user'] and index['valid'] and index['ready'] and index['predicate'] is None
                assert 'USING hnsw (vec vector_cosine_ops)' in index['definition'] and index['bytes']==7151616
                save(path/'catalog.json',meta)
                snapshot=op.execute(db,fingerprints_sql(d),phase='metadata',label=1)[0][0]['snapshot']
                for scale,expected_n in [('current',33371),('10000',43236),('30000',129758)]:
                    expected=d['oldpre']['source_fingerprint'] if scale=='current' else d['oldfinal']['targets'][PREFIXES[scale]]['fingerprint']
                    assert snapshot[scale]['facts']==expected and snapshot[scale]['facts']['n']==expected_n
                    assert snapshot[scale]['fact_vectors']['n']=={'current':3462,'10000':4480,'30000':13444}[scale]
                save(path/'snapshot.json',snapshot)
                out['status']='METADATA_VERIFIED'
        elif stage in ('test-red','test-green'):
            import test_ann_shape as tests
            if stage=='test-green':
                hashes=freeze('source-green')
                assert 'ROWS WITH TIES' in (HERE/'ann_shape.py').read_text()
                save(OWN/'candidate-freeze.json',dict(source_hashes=hashes,only_semantic_repair='inner ONLY becomes WITH TIES',
                      query_templates=[dict(scale=s,case=c['id'],query=candidate(Deployment(d['endpoint'],p),d['norms'][c['id']],c['query'],d['vectors'][c['id']])) for s,p in PREFIXES.items() for c in (d['cases'] if s=='current' else d['selected'])],
                      argv=sys.orig_argv))
            with connect(d,out,exact=True) as db:
                tests.LIVE=(db,op)
                suite=unittest.TestSuite([unittest.TestLoader().loadTestsFromTestCase(tests.Helpers),tests.live_suite()])
                stream=io.StringIO();result=unittest.TextTestRunner(stream=stream,verbosity=2).run(suite)
                write_new_text(path/'tests.log',stream.getvalue())
                out.update(tests=result.testsRun,failures=len(result.failures),errors=len(result.errors),skipped=len(result.skipped))
                if stage=='test-red':
                    assert result.failures and not result.errors,'RED must be intended semantic assertion, no setup/import errors'
                    out['status']='EXPECTED_SEMANTIC_RED';code=1
                else:
                    assert result.wasSuccessful() and not result.skipped
                    out['status']='GREEN';code=0
        else:
            run(d,out,op,path)
        if stage not in ('test-red','test-green'):code=0
    except BaseException as error:
        out.update(error_type=type(error).__name__,status='FAILED')
        write_new_text(path/'failure.txt',traceback.format_exc())
    finally:
        after=resource.getrusage(resource.RUSAGE_SELF)
        op.close()
        # Saving is outside query execution budget; partial packets survive errors.
        out.pop('connection_lock')
        out.update(exit=code,actual_attempted_sql=op.count,wall_s=time.monotonic()-start,
                   user_s=after.ru_utime-before.ru_utime,system_s=after.ru_stime-before.ru_stime,
                   client_highwater_rss_kib=after.ru_maxrss,source_hashes=identities())
        save(path/'result.json',out)
    print('STAGE '+stage+': '+out['status']+'; SQL attempts='+str(op.count)+'; exit='+str(code),flush=True)
    return code


def run(d,out,op,path):
    frozen=read(OWN/'candidate-freeze.json',{'candidate-freeze.json'})
    assert identities()==frozen['source_hashes']
    assert read(OWN/'test-green/result.json',{'result.json'})['status']=='GREEN'
    assert read(OWN/'metadata/result.json',{'result.json'})['status']=='METADATA_VERIFIED'
    dep=Deployment(d['endpoint'],PREFIXES['30000'])
    c=d['selected'][0];n=d['norms'][c['id']];v=d['vectors'][c['id']]
    # Fixed four diagnostic contrasts; no retrospective replacement or forcing.
    raw='SELECT v.id,v.version,v.vec <=> %s::public.vector distance FROM '+dep.table('fact_vectors')+' v ORDER BY v.vec <=> %s::public.vector'
    q,p=reference(dep,n,c['query'],v);cq,cp=candidate(dep,n,c['query'],v)
    diagnostics=[(q,p),(raw+' LIMIT 10',[literal(v),literal(v)]),(raw+',v.id LIMIT 10',[literal(v),literal(v)]),(cq,cp)]
    plans=[]
    with connect(d,out) as db:
        for ordinal,(q,p) in enumerate(diagnostics):
            plan=op.execute(db,'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+q,p,phase='diagnostic',label=ordinal)[0][0]['QUERY PLAN'][0]
            plans.append(dict(ordinal=ordinal,**plan_projection(plan,INDEX)))
            save(path/f'diagnostic-{ordinal}.json',plan)
    out['diagnostic']=plans
    checks=[]
    # The reference remains byte-for-byte compiled by the accepted helper.
    for scale,prefix in PREFIXES.items():
        dep=Deployment(d['endpoint'],prefix)
        with connect(d,out,exact=True) as db:
            for ordinal,c in enumerate(d['cases'] if scale=='current' else d['selected']):
                n=d['norms'][c['id']];v=d['vectors'][c['id']]
                truth=op.execute(db,*reference(dep,n,c['query'],v),phase='correctness-reference',label=[scale,ordinal])[0]
                saved=d['oracles'][scale][c['id']];eligible=d['eligible'][scale][c['id']]
                saved_check=agreement(truth,saved,eligible)
                hits=op.execute(db,*candidate(dep,n,c['query'],v),phase='correctness-candidate',label=[scale,ordinal])[0]
                check=agreement(hits,truth,eligible)
                row=dict(scale=scale,ordinal=ordinal,case=c['id'],reference=truth,hits=hits,
                         saved_oracle=saved_check,check=check)
                save(path/f'correctness-{scale}-{ordinal:03}.json',row)
                assert saved_check['exact_equal'] and saved_check['eligible'] and check['exact_equal'] and check['eligible'],'exact equality mismatch'
                checks.append(dict(scale=scale,**check))
    out['correctness']=dict(contexts=len(checks),all_equal=True,all_eligible=True,score_tolerance=3e-6,
                            compared_saved_oracle=True,by_scale={s:sum(r['scale']==s for r in checks) for s in PREFIXES})
    dep=Deployment(d['endpoint'],PREFIXES['30000']);coverage=[]
    for ef in (40,100):
        with connect(d,out,ef=ef) as db:
            for ordinal,c in enumerate(d['selected']):
                q,p=candidate(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
                plan=op.execute(db,'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+q,p,phase='coverage',label=[ef,ordinal])[0][0]['QUERY PLAN'][0]
                save(path/f'coverage-{ef}-{ordinal}.json',plan)
                coverage.append(dict(ef=ef,ordinal=ordinal,**plan_projection(plan,INDEX)))
    out['coverage']=coverage
    if not all(p['hnsw_used'] for p in coverage):
        out['status']='INDEX_NOT_USED'
        op.skip('timing','INDEX_NOT_USED')
    else:
        # One cohort, fixed group order; no recall/latency tuning or rerun.
        samples=[];warmups=[];groups=[];sample_lock=threading.Lock()
        try:
            for concurrency in (1,2):
                for arm,ef in [('reference',40),('ann-40',40),('ann-100',100)]:
                    started=time.monotonic()
                    def worker(chunk):
                        with connect(d,out,ef=ef) as db:
                            for rep in range(-1,3):
                                for ordinal,c in chunk:
                                    compiler=reference if arm=='reference' else candidate
                                    q,p=compiler(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
                                    hits,ms=op.execute(db,q,p,phase='warmup' if rep==-1 else 'timing',label=[arm,concurrency,rep,ordinal])
                                    quality=agreement(hits,d['oracles']['30000'][c['id']],d['eligible']['30000'][c['id']])
                                    row=dict(arm=arm,concurrency=concurrency,rep=rep,ordinal=ordinal,sql_ms=ms,executed_sql=True,hits=hits,quality=quality)
                                    with sample_lock:
                                        (warmups if rep==-1 else samples).append(row)
                    with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
                        list(pool.map(worker,[list(enumerate(d['selected']))[i::concurrency] for i in range(concurrency)]))
                    groups.append(dict(arm=arm,concurrency=concurrency,wall_s=time.monotonic()-started))
        finally:
            # Keep successful results and quality even if a later worker fails.
            save(path/'timings.json',samples);save(path/'warmups.json',warmups)
            out.update(warmup_sql=len(warmups),timed_sql=len(samples))
        out['timings']=[]
        for g in groups:
            rows=[r for r in samples if (r['arm'],r['concurrency'])==(g['arm'],g['concurrency'])]
            vals=[r['sql_ms'] for r in rows];qs=[r['quality'] for r in rows]
            mean=lambda k:sum(x[k] for x in qs if x[k] is not None)/sum(x[k] is not None for x in qs) if any(x[k] is not None for x in qs) else None
            out['timings'].append(dict(**g,samples=len(vals),p50_ms=nearest(vals,.5),p95_ms=nearest(vals,.95),p99_ms=nearest(vals,.99),
                  strict_id_version_recall_at10_mean=mean('strict_id_version_recall_at10'),boundary_valid_fraction_mean=mean('boundary_valid_fraction'),
                  all_eligible=all(x['eligible'] for x in qs),underfilled_requests=sum(x['underfill']>0 for x in qs),
                  underfilled_total=sum(x['underfill'] for x in qs),ordered_identity_equal_requests=sum(x['exact_equal'] for x in qs)))
        out.update(status='ANN_EXERCISED',warmup_sql=len(warmups),timed_sql=len(samples))
    with connect(d,out) as db:
        final=op.execute(db,fingerprints_sql(d),phase='final-metadata',label=0)[0][0]['snapshot']
    save(path/'final-snapshot.json',final)
    out['targets_unchanged']=final==read(OWN/'metadata/snapshot.json',{'snapshot.json'})
    assert out['targets_unchanged']
    # Recheck exact cached inputs and seal identity after the study.
    after=inputs();assert after['used']==d['used'] and after['seal_sha256']==d['seal_sha256']
    out['inputs_unchanged']=True


def main():
    os.umask(0o077)
    try:
        if sys.argv[1]=='declare':declare();return 0
        return stage(sys.argv[1])
    except BaseException:
        # Do not write into a previous attempt; fixed console category only.
        print('FAILED: declaration or output collision; existing packets preserved.',file=sys.stderr)
        return 2

if __name__=='__main__':sys.exit(main())
