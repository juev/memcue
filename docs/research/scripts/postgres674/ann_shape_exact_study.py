"""One new, prospectively declared read-only attempt. No earlier packet is edited."""
import time
ENTRY=time.monotonic()
import concurrent.futures
import contextlib
import io
import json
import os
from pathlib import Path
import resource
import signal
import subprocess
import sys
import threading
import traceback
import unittest
from unittest.mock import patch

import psycopg
from psycopg.rows import dict_row
from ann_shape import agreement,candidate,reference,plan_projection,save
from ann_shape_exact import WholeBudget,DeadlineExceeded,Operations,Journal,compile_request
import ann_shape_study as previous
from deployed import Deployment
from embed import TAG
from matched_query import safe_read,sha,nearest
from outputs import create_output_directory,write_new_text

HERE=Path(__file__).absolute().parent
ROOT=HERE.parents[3]
OWN=ROOT/'.research-private/local-ann-exact-02'
PRIOR=ROOT/'.research-private/local-ann-shape-01'
HEAD='3c827fd2c4eb065485fb6e30d21d659d7802da90'
PREFIXES=previous.PREFIXES
INDEX=previous.INDEX
MAX_SQL=565
NEW_SOURCES=('ann_shape_exact.py','ann_shape_exact_study.py','test_ann_shape_exact.py')
DEPENDENCIES=('ann_shape.py','ann_shape_study.py','test_ann_shape.py','backend.py','deployed.py','embed.py','matched_query.py','outputs.py')


def identities():
    return {n:sha(safe_read(HERE,n,set(NEW_SOURCES+DEPENDENCIES))) for n in NEW_SOURCES+DEPENDENCIES}


def load_inputs():
    d=previous.inputs()
    manifest=json.loads(safe_read(PRIOR,'manifest-final.json',{'manifest-final.json'}))
    assert len(manifest['files'])==119
    allow={r['path']:r for r in manifest['files']}
    def prior(name):
        b=safe_read(PRIOR,name,allow)
        assert sha(b)==allow[name]['sha256'] and len(b)==allow[name]['bytes']
        return json.loads(b)
    d['snapshot']=prior('metadata/snapshot.json')
    frozen=prior('candidate-freeze.json')
    assert sha(safe_read(PRIOR,'manifest-final.json',{'manifest-final.json'}))
    by_key={(r['scale'],r['case']):r['query'] for r in frozen['query_templates']}
    for scale,prefix in PREFIXES.items():
        dep=Deployment(d['endpoint'],prefix)
        for c in d['cases'] if scale=='current' else d['selected']:
            q,p=candidate(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
            assert [q,p]==by_key[(scale,c['id'])]
    # All old virtual WORKTREE entries describe the frozen committed tree, not
    # a promise that later live helpers can never evolve. No old seal is edited.
    d['prior_seal_sha256']=sha(safe_read(PRIOR,'manifest-final.json',{'manifest-final.json'}))
    return d


def declare():
    assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()==HEAD
    assert subprocess.check_output(['git','branch','--show-current'],text=True).strip()=='hermes/memcue-postgres-674'
    assert not (OWN/'attempt').exists() and not (OWN/'source').exists()
    d=load_inputs();out=create_output_directory(OWN/'source');hashes=identities()
    for n in hashes:write_new_text(out/n,safe_read(HERE,n,set(hashes)).decode())
    variants=[]
    for scale,prefix in PREFIXES.items():
        dep=Deployment(d['endpoint'],prefix)
        for ordinal,c in enumerate(d['cases'] if scale=='current' else d['selected']):
            args=(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
            variants.append(dict(scale=scale,ordinal=ordinal,case=c['id'],reference=compile_request('reference',*args),
                                 default_candidate=compile_request('default-candidate',*args),exact_candidate=compile_request('exact-candidate',*args)))
    public=dict(starting_head=HEAD,production_baseline='a5de0b989ceb1046f4933b19cae7ef60bed0ede2',model_tag=TAG,
      method='Correctness-only inner cosine ordering adds floating zero; score expression, bindings, eligibility and both timed compilers are unchanged. Ordinary B-tree/bitmap paths remain enabled.',
      alternatives='Global scan disabling rejected by preserved timeout. Materialized vector input not selected; no adaptive method choice after results.',
      selection='Same saved128current and fixed8per replica; saved order, vectors, filters, dates, modeltag, normalizer, limit10 and oracle. Empty lexical context remains a vector request.',
      fixtures='Revised six real PG VALUES methods: default candidate baseline, exact candidate baseline, three independent latest/status/expiry guard mutations on exact candidate. Twelve SELECTs per suite,60total. Every lost guard must cause semantic assertions, zero setup/errors.',
      representative_plans='Before corpus gate: first saved context current and first two selected30k, each natural default reference and correctness-only candidate,6EXPLAIN ANALYZE. Both noHNSW, candidate ordinary history index access required.',
      correctness=dict(current=128,replica10000=8,replica30000=8,ranked_selects=288,tolerance=3e-6,strict_id_version_order=True,
                       eligible='Saved eligible sets and exact oracle; full initial/final five-table fingerprints/counts match the previously sealed snapshot, and live exact reference matches saved oracle.'),
      coverage='After equality,16default candidate EXPLAIN ANALYZE: all8selected contexts at ef40/100. No forced plan or changed planner costs. Preparation disabled everywhere. Coverage is per probe, not every later execution.',
      cohort=dict(groups=6,contexts=8,warmups=48,timed=144,requests=192,repetitions=3,concurrency=[1,2],arms=['reference','ann-40','ann-100'],
                  schedule='Concurrency1then2; reference thenann40thenann100; workers take saved ordinal modulo concurrency; each worker warmups its chunk then3ordered repetitions. One cohort maximum; no rerun.',
                  stop='Incomplete equality/plan/index gate means no cohort. Reference identity mismatch stops cohort; poor ANN recall/underfill remains evidence, no tuning.'),
      accounting=dict(maximum_sql=565,hard_user_cap=600,initial_metadata=2,final_metadata=1,fixture_selects=60,representative_explain=6,
                      correctness_selects=288,coverage_explain=16,warmup_selects=48,timed_selects=144,session_startup_sql=0,
                      note='libpq startup options/protocol messages are not SQL operations. All issued SELECT/EXPLAIN attempts are started/finished in one ledger; failed/absent timing is null, no placeholders.'),
      limits=dict(whole_driver_wall_s=300,includes='launcher before interpreter/import/input loading/admission; one monotonic deadline never reset at stages',
                  work_reserve_s=15,query_timeout_s=5,query_admission_minimum_s=5.5,clients=2,parallel_workers=0,work_mem_mib=64,maintenance_work_mem_mib=64),
      startup='Autocommit; default_transaction_read_only=on; statement_timeout5000; lock_timeout5000; parallel0; jitoff; work/maintenance64MB; ef40or100; no index/bitmap disabling, forcing or iterative-mode change.',
      preparation='prepare_threshold=None and prepare=False; no PREPARE/generic/custom test. Every template/parameter frozen privately before SQL.',
      sources=hashes,provider_calls=0,new_embeddings=0,new_native_episodes=0,server_resources='UNKNOWN',
      limitations='Development case reuse; ANN ties may miss identities even with WITH TIES. Strict identity recall and3e-6boundary validity differ.24dependent timings/group p95rank23,p99/maxrank24, not stable tails or natural quality.',
      historical='Original31SQL, old snapshots, failed reviews and102/7/119seals remain immutable; old virtual entries refer to their frozen trees. New source/declaration/packet identities are separate.')
    save(HERE/'ann_shape-exact-declaration.json',public)
    save(OWN/'private-declaration.json',dict(argv=sys.orig_argv,cli_thread_id=os.environ.get('CODEX_THREAD_ID'),source_hashes=hashes,
       public_sha256=sha((HERE/'ann_shape-exact-declaration.json').read_bytes()),prior_seal_sha256=d['prior_seal_sha256'],
       cache_hashes=d['cfg']['input_hashes'],selected=d['cfg']['selected'],variants=variants,created_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
    print('DECLARED:565maximum SQL; one300s whole deadline; B-tree paths retained; one gated cohort.')


@contextlib.contextmanager
def connect(d,budget,connections,ef=40,final=False):
    budget.stage('connect',reserve=0 if final else 15)
    credential=ROOT/'.research-private/loffe-research.pgpass'
    for p in (credential,*credential.parents):
        if p.is_symlink():raise ValueError('credential alias forbidden')
    assert credential.stat().st_mode&0o777==0o600
    os.environ['PGPASSFILE']=str(credential) # libpq alone reads credential bytes.
    options=('-c default_transaction_read_only=on -c statement_timeout=5000 -c lock_timeout=5000 '
             '-c max_parallel_workers_per_gather=0 -c jit=off -c work_mem=64MB '
             '-c maintenance_work_mem=64MB -c hnsw.ef_search='+str(ef))
    start=time.monotonic()
    db=psycopg.connect(**d['endpoint'],options=options,connect_timeout=5,autocommit=True,row_factory=dict_row,prepare_threshold=None)
    connections.write(dict(connection_ms=(time.monotonic()-start)*1000,options=options,ef=ef,prepare_threshold=db.prepare_threshold))
    try:yield db
    finally:db.close()


def query(op,db,arm,dep,d,c,phase,label):
    return op.execute(db,*compile_request(arm,dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']]),phase=phase,label=label)


def fixtures(db,op,path,results,budget,only=None):
    import test_ann_shape as tests
    baseline=candidate
    suites=[('default',baseline),('exact',lambda *a,**k:compile_request('exact-candidate',*a,**k))]
    for name,old,new in [('latest','f.version=(SELECT max(version) FROM facts z WHERE z.id=f.id)','true'),
                         ('status',"f.status='active'",'true'),('expiry',"(f.expires='' OR f.expires>=%s)",'(%s::text IS NOT NULL)')]:
        def mutant(*a,_old=old,_new=new,**kw):
            q,p=compile_request('exact-candidate',*a,**kw)
            assert q.count(_old)==1
            return q.replace(_old,_new),p
        suites.append((name,mutant))
    for name,compiler in suites:
        if only is not None and name!=only:continue
        budget.stage('fixtures-'+name)
        stream=io.StringIO()
        with patch.object(tests,'candidate',compiler),patch.object(tests,'LIVE',(db,op)):
            r=unittest.TextTestRunner(stream=stream,verbosity=2).run(tests.live_suite())
        write_new_text(path/('fixtures-'+name+'.log'),stream.getvalue())
        record=dict(suite=name,tests=r.testsRun,failures=len(r.failures),errors=len(r.errors),skipped=len(r.skipped))
        results.write(dict(type='fixture',**record))
        assert r.testsRun==6 and not r.errors and not r.skipped
        assert (not r.failures if name in ('default','exact') else bool(r.failures)),'guard control was not detected'


def explain(op,db,q,p,phase,label,results,path):
    plan=op.execute(db,'EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) '+q,p,phase=phase,label=label)[0][0]['QUERY PLAN'][0]
    projection=plan_projection(plan,INDEX)
    def history_index(n):
        return (n.get('Relation Name','').endswith('_facts') and n.get('Node Type') in ('Index Scan','Index Only Scan') or
                any(history_index(c) for c in n.get('Plans',[])))
    projection['history_index_access']=bool(history_index(plan['Plan']))
    results.write(dict(type='plan',phase=phase,label=label,projection=projection))
    return projection


def cohort(d,op,budget,path,results,connections):
    dep=Deployment(d['endpoint'],PREFIXES['30000'])
    for concurrency in (1,2):
        for arm,ef in [('reference',40),('ann-40',40),('ann-100',100)]:
            budget.stage('cohort-group')
            results.write(dict(type='group-start',arm=arm,concurrency=concurrency,ef=ef))
            def worker(chunk):
                with connect(d,budget,connections,ef) as db:
                    for rep in range(-1,3):
                        for ordinal,c in chunk:
                            phase='warmup' if rep==-1 else 'timing'
                            hits,ms=query(op,db,arm,dep,d,c,phase,[arm,concurrency,rep,ordinal])
                            check=agreement(hits,d['oracles']['30000'][c['id']],d['eligible']['30000'][c['id']])
                            results.write(dict(type=phase,arm=arm,concurrency=concurrency,rep=rep,ordinal=ordinal,
                                               executed_sql=True,sql_ms=ms,hits=hits,quality=check))
                            if arm=='reference':assert check['exact_equal'],'timed natural reference changed'
                            assert check['eligible'],'ineligible timed result'
            with concurrent.futures.ThreadPoolExecutor(max_workers=concurrency) as pool:
                list(pool.map(worker,[list(enumerate(d['selected']))[i::concurrency] for i in range(concurrency)]))
            results.write(dict(type='group-finished',arm=arm,concurrency=concurrency))


def execute(budget,path,loader=load_inputs,resume=False):
    out=dict(status='INCOMPLETE',exit=1,source_hashes=identities(),sql_started=0,sql_completed=0,sql_failed=0)
    before=resource.getrusage(resource.RUSAGE_SELF)
    op=Operations(path/'operations.jsonl',budget);results=Journal(path/'results.jsonl');connections=Journal(path/'connections.jsonl')
    d=None;initial=None
    save(path/'start.json',dict(argv=sys.orig_argv,launcher_started=budget.started,absolute_deadline=budget.deadline,
                               entry=ENTRY,source_hashes=identities(),cli_thread_id=os.environ.get('CODEX_THREAD_ID')))
    try:
        budget.stage('before-input-loading')
        d=loader()
        budget.stage('after-input-loading')
        declaration_name='continuation-declaration.json' if resume else 'private-declaration.json'
        frozen=json.loads(safe_read(OWN,declaration_name,{declaration_name}))
        assert identities()==frozen['source_hashes'] and d['prior_seal_sha256']==frozen['prior_seal_sha256']
        with connect(d,budget,connections) as db:
            if resume:
                # Reuse only sealed, successful initial readbacks from this same
                # absolute-deadline invocation; the failed expiry control is kept.
                initial=json.loads(safe_read(OWN/'attempt','initial-snapshot.json',{'initial-snapshot.json'}))
                assert initial==d['snapshot']
                save(path/'initial-snapshot.json',initial)
            else:
                meta=op.execute(db,"""SELECT (%s::public.vector <=> %s::public.vector) control_distance,
                  current_setting('server_version') server_version,(SELECT extversion FROM pg_extension WHERE extname='vector') vector_version,
                  current_setting('default_transaction_read_only') read_only,current_setting('enable_indexscan') indexscan,
                  current_setting('enable_bitmapscan') bitmapscan,current_setting('max_parallel_workers_per_gather') parallel_workers,
                  current_setting('statement_timeout') statement_timeout,current_setting('work_mem') work_mem,
                  current_setting('maintenance_work_mem') maintenance_work_mem,current_setting('hnsw.iterative_scan',true) iterative_scan,
                  (SELECT jsonb_agg(jsonb_build_object('name',i.relname,'valid',ix.indisvalid,'ready',ix.indisready,
                    'predicate',pg_get_expr(ix.indpred,ix.indrelid),'definition',pg_get_indexdef(i.oid),'bytes',pg_relation_size(i.oid),
                    'owned',i.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)))
                   FROM pg_index ix JOIN pg_class i ON i.oid=ix.indexrelid JOIN pg_class t ON t.oid=ix.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace
                   WHERE n.nspname='research' AND t.relname='p2_m674s01b_fact_vectors') indexes""",
                  ['[1,'+','.join(['0']*383)+']']*2,phase='initial-metadata',label=0)[0][0]
                assert meta['read_only']=='on' and meta['indexscan']==meta['bitmapscan']=='on' and meta['parallel_workers']=='0'
                index=next(r for r in meta['indexes'] if r['name']==INDEX)
                assert index['valid'] and index['ready'] and index['owned'] and index['predicate'] is None and index['bytes']==7151616
                assert 'USING hnsw (vec vector_cosine_ops)' in index['definition']
                save(path/'metadata.json',meta)
                initial=op.execute(db,previous.fingerprints_sql(d),phase='initial-metadata',label=1)[0][0]['snapshot']
                save(path/'initial-snapshot.json',initial);assert initial==d['snapshot'],'previous sealed data changed'
            budget.stage('real-pg-fixtures');fixtures(db,op,path,results,budget,only='expiry' if resume else None)
            budget.stage('representative-exact-plans')
            for scale,cases in [('current',d['selected'][:1]),('30000',d['selected'][:2])]:
                dep=Deployment(d['endpoint'],PREFIXES[scale])
                for ordinal,c in enumerate(cases):
                    for arm in ('reference','exact-candidate'):
                        q,p=compile_request(arm,dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
                        projection=explain(op,db,q,p,'representative',[scale,ordinal,arm],results,path)
                        assert not projection['hnsw_used'],'exact comparator used HNSW'
                        if arm=='exact-candidate':assert projection['history_index_access'],'ordinary history index access absent'
        out['correctness_pairs']=0
        for scale,prefix in PREFIXES.items():
            budget.stage('correctness-'+scale)
            dep=Deployment(d['endpoint'],prefix)
            with connect(d,budget,connections) as db:
                for ordinal,c in enumerate(d['cases'] if scale=='current' else d['selected']):
                    budget.stage('correctness-pair')
                    truth=query(op,db,'reference',dep,d,c,'correctness-reference',[scale,ordinal])[0]
                    eligible=d['eligible'][scale][c['id']]
                    saved=agreement(truth,d['oracles'][scale][c['id']],eligible)
                    results.write(dict(type='correctness-reference',scale=scale,ordinal=ordinal,hits=truth,quality=saved))
                    assert saved['exact_equal'] and saved['eligible'],'live/saved oracle disagreement'
                    hits=query(op,db,'exact-candidate',dep,d,c,'correctness-candidate',[scale,ordinal])[0]
                    check=agreement(hits,truth,eligible)
                    results.write(dict(type='correctness-candidate',scale=scale,ordinal=ordinal,hits=hits,quality=check))
                    assert check['exact_equal'] and check['eligible'],'exact candidate mismatch'
                    out['correctness_pairs']+=1
        assert out['correctness_pairs']==144
        budget.stage('default-index-coverage');coverage=[]
        dep=Deployment(d['endpoint'],PREFIXES['30000'])
        for ef in (40,100):
            with connect(d,budget,connections,ef) as db:
                for ordinal,c in enumerate(d['selected']):
                    q,p=compile_request('default-candidate',dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])
                    coverage.append(explain(op,db,q,p,'coverage',[ef,ordinal],results,path)['hnsw_used'])
        out['coverage']=dict(plans=len(coverage),hnsw_used=sum(coverage))
        if not all(coverage):out['status']='INDEX_NOT_USED'
        else:
            budget.stage('single-cohort')
            results.write(dict(type='cohort-start'))
            cohort(d,op,budget,path,results,connections)
            out['status']='COHORT_COMPLETE'
        out['exit']=0
    except BaseException as error:
        out.update(status='FAILED',error_type=type(error).__name__)
        write_new_text(path/'failure.txt',traceback.format_exc())
    finally:
        # Reserved final readback runs even after a failed gate, when affordable.
        if d is not None:
            try:
                budget.stage('final-readback',reserve=0)
                with connect(d,budget,connections,final=True) as db:
                    final=op.execute(db,previous.fingerprints_sql(d),phase='final-metadata',label=0,reserve=0)[0][0]['snapshot']
                save(path/'final-snapshot.json',final)
                out['fingerprints_unchanged']=initial is not None and final==initial==d['snapshot']
                if not out['fingerprints_unchanged']:out.update(status='FAILED',exit=1,final_error='fingerprint mismatch')
            except BaseException as error:
                out.update(status='FAILED',exit=1,final_readback_error=type(error).__name__)
                write_new_text(path/'final-readback-failure.txt',traceback.format_exc())
        after=resource.getrusage(resource.RUSAGE_SELF)
        out.update(sql_started=budget.count,sql_completed=op.completed,sql_failed=op.failed,
                   whole_wall_s=time.monotonic()-budget.started,deadline=budget.deadline,
                   client_user_s=after.ru_utime-before.ru_utime,client_system_s=after.ru_stime-before.ru_stime,
                   client_highwater_rss_kib=after.ru_maxrss,source_final_hashes=identities())
        op.close();results.close();connections.close()
        save(path/'result.json',out)
    return out['exit']


def main():
    os.umask(0o077)
    if sys.argv[1]=='declare':declare();return 0
    if sys.argv[1]=='continue-declare':
        original=json.loads(safe_read(OWN,'private-declaration.json',{'private-declaration.json'}))
        hashes=identities();src=create_output_directory(OWN/'source-02')
        for n in hashes:write_new_text(src/n,safe_read(HERE,n,set(hashes)).decode())
        original.update(source_hashes=hashes,sql_ceiling=578,prior_attempted=63,
                        correction='Test-local expiry mutant preserves parameter count and binds expiry date explicitly as text; baseline/candidate/query/ANN parameters unchanged.',
                        reused_successful=['initial metadata and fingerprints','default/exact baseline fixtures','latest/status mutation controls'],
                        no_deadline_reset=True,no_cohort_rerun=True,argv=sys.orig_argv)
        save(OWN/'continuation-declaration.json',original)
        save(HERE/'ann_shape-exact-control-supplement.json',dict(maximum_total_sql=578,previous_attempted=63,remaining_maximum=515,
             corrected_expiry_control_selects=12,representative_explain=6,correctness_selects=288,coverage_explain=16,cohort_requests=192,final_metadata=1,
             whole_deadline_reset=False,source_hashes=hashes,new_cohorts_permitted=1,
             correction='Explicit text cast only in intentionally faulty expiry mutation; six original datatype errors remain failed evidence. Reuse successful controls, preserve both attempt packets. All use same original300second absolute clock.'))
        print('CONTINUATION DECLARED:578total maximum; original absolute deadline; no cohort has run.');return 0
    started=float(os.environ.get('MEMCUE_674_EXACT_STARTED',str(ENTRY)))
    resume=sys.argv[1]=='continue-run'
    budget=WholeBudget(started,ceiling=578 if resume else MAX_SQL)
    if resume:budget.count=63
    # Main-thread fail-safe leaves a two-second packet-finalization margin.
    def deadline(signum,frame):raise DeadlineExceeded('whole driver wall boundary')
    signal.signal(signal.SIGALRM,deadline)
    signal.setitimer(signal.ITIMER_REAL,max(.001,budget.remaining()-2))
    try:
        path=create_output_directory(OWN/('attempt-02' if resume else 'attempt'))
        code=execute(budget,path,resume=resume)
        print('FINITE ATTEMPT SAVED: exit='+str(code)+'; SQLattempts='+str(budget.count),flush=True)
        return code
    finally:signal.setitimer(signal.ITIMER_REAL,0)

if __name__=='__main__':sys.exit(main())
