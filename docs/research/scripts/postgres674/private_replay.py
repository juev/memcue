"""Frozen private replay against explicitly provisioned PostgreSQL tables.

Run this command through build-check.sh. It never invokes a wrapper itself.
Only aggregate allowlisted output from recompute_private.py may be published.
"""
import argparse
import concurrent.futures
import contextlib
import hashlib
import json
import os
from pathlib import Path
import resource
import subprocess
import sys
import time

from backend import COLUMNS
from deployed import Deployment
from embed import Embedder, TAG, content_hash
from normalize import Normalizer
from outputs import new_output_directory, write_new_text
from private_corpus import candidate_history, latest_facts, select_contexts, verify_inputs
from source_identity import verify_bridge, verify_source

HERE = Path(__file__).resolve().parent
ARMS = ('sqlite-full','sqlite-content','pg-lexical','pg-vector-exact','pg-hybrid-rrf60')


def save(path, value):
    write_new_text(path, json.dumps(value, ensure_ascii=False, indent=2)+'\n')


def digest(value):
    return hashlib.sha256(json.dumps(value,ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()).hexdigest()


class Journal:
    def __init__(self, path):
        self.file = path.open('x', encoding='utf-8'); path.chmod(0o600)
    def write(self, value):
        self.file.write(json.dumps(value, ensure_ascii=False)+'\n'); self.file.flush()
    def close(self): self.file.close()


def usage():
    a = resource.getrusage(resource.RUSAGE_SELF); b = resource.getrusage(resource.RUSAGE_CHILDREN)
    return dict(cpu_user_s=a.ru_utime, cpu_system_s=a.ru_stime, process_highwater_rss_kib=a.ru_maxrss,
                children_cpu_user_s=b.ru_utime, children_cpu_system_s=b.ru_stime, children_highwater_rss_kib=b.ru_maxrss)


@contextlib.contextmanager
def stage(name, journal):
    start = time.monotonic(); before = usage()
    try: yield
    finally:
        after = usage()
        journal.write(dict(stage=name,wall_s=time.monotonic()-start,
                           **{key:(after[key] if 'highwater' in key else after[key]-before[key]) for key in after}))


def invoke(argv, out, name, timeout=300):
    start = time.monotonic()
    p = subprocess.run([str(x) for x in argv],capture_output=True,text=True,timeout=timeout)
    save(out/(name+'.json'),dict(argv=[str(x) for x in argv],exit=p.returncode,wall_s=time.monotonic()-start,stdout=p.stdout,stderr=p.stderr))
    if p.returncode: raise RuntimeError('native control failed; see owned evidence')
    return p.stdout


def project_hits(reply):
    return [dict(id=h['ID'],version=h['Version'],score=h['BM25']) for h in reply.get('hits') or []]


def sqlite_query(bridge, db, query):
    start = time.perf_counter()
    p = subprocess.run([str(bridge),'search-db',str(db)],input=json.dumps(query),text=True,capture_output=True,check=True,timeout=30)
    reply = json.loads(p.stdout)
    return dict(hits=project_hits(reply),sql_ms=reply['ms'],research_cli_ms=(time.perf_counter()-start)*1000)


def freeze(args, out, cases, manifest):
    identity = verify_source(HERE.parents[3]); native = verify_bridge(args.bridge,identity,HERE/'sqlite_bridge.go')
    declaration = json.loads((HERE/'phase2-declaration.json').read_text())
    source = out/'source'; source.mkdir(mode=0o700)
    hashes = {}
    for p in sorted(HERE.iterdir()):
        if p.is_file():
            data = p.read_bytes(); write_new_text(source/p.name,data.decode())
            hashes[p.name] = hashlib.sha256(data).hexdigest()
    model = json.loads((Path(args.model_cache)/'provenance.json').read_text())
    for row in model['files']:
        if hashlib.sha256((Path(args.model_cache)/Path(row['file']).name).read_bytes()).hexdigest()!=row['sha256']:
            raise ValueError('model hash mismatch')
    frozen = dict(declaration=declaration,declaration_sha256=hashlib.sha256((HERE/'phase2-declaration.json').read_bytes()).hexdigest(),
                  source_identity=identity,native_bridge=native,source_hashes=hashes,model=model,model_tag=TAG,
                  input_manifest_sha256=hashlib.sha256((Path(args.input)/'manifest.json').read_bytes()).hexdigest(),
                  context_sha256=digest(cases),contexts=cases,table_prefix=args.prefix,endpoint=json.loads(Path(args.endpoint).read_text()),
                  argv=sys.orig_argv,started_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()),
                  snapshot_consistency=manifest['snapshot_consistency'],parent_mutable_exception='.research-private/checkpoint.json',
                  environment=dict(python=sys.version,go=subprocess.check_output(['go','version'],text=True).strip(),
                                   machine=os.uname().machine,cgroup_cpu=Path('/sys/fs/cgroup/cpu.max').read_text().strip(),
                                   cgroup_memory=Path('/sys/fs/cgroup/memory.max').read_text().strip()))
    save(out/'freeze.json',frozen); save(out/'cases.json',cases)
    return frozen


def negative_controls(args, out):
    # Fresh invented fixtures, never private-derived labels. No semantic vectors.
    f = dict(id='synthetic-control',version=1,content='primaryanchor',keywords='auxiliaryanchor',queries='queryanchor',
             origin='',created='2026-01-01T00:00:00Z',status='active',author='fixture',expires='',kind='rule',scope='global',
             source='tool',evidence='metadataanchor',review='',prompt_version=0,channel='')
    cases=[]
    for word in ('primaryanchor','auxiliaryanchor','queryanchor','metadataanchor'):
        cases.append(dict(id=word,query=dict(Text=word,N=10,MinScore=0,Project=[],Host='synthetic-caller',In=['all'],Today='2026-11-06')))
    save(out/'negative-fixture.json',dict(facts=[f],cases=cases))
    invoke([args.bridge,out/'negative-fixture.json',out/'negative-sqlite'],out,'negative-native')
    results=json.loads((out/'negative-sqlite/sqlite-results.json').read_text())
    for row in results:
        expect = row['case']=='primaryanchor' or (row['arm']=='sqlite-full' and row['case'] in ('auxiliaryanchor','queryanchor'))
        assert bool(row['hits'])==expect, 'field isolation failure'
    save(out/'negative-controls.json',dict(passed=True,assertions=len(results)))


def histories(out, latest, facts):
    for arm in ('sqlite-full','sqlite-content'):
        rows=json.loads((out/'sqlite'/f'{arm}-history.json').read_text())
        assert len(rows)==len(facts)
    assert (out/'sqlite/sqlite-full-history.json').read_bytes()==(out/'sqlite/sqlite-content-history.json').read_bytes()
    rows=json.loads((out/'sqlite/sqlite-results.json').read_text())
    for arm in ('sqlite-full','sqlite-content'):
        a=[(r['case'],r['hits']) for r in rows if r['arm']==arm and r['rep']==0]
        b=[(r['case'],r['hits']) for r in rows if r['arm']==arm and r['rep']==1]
        assert a==b,'baseline-repeat instability'
    save(out/'integrity-native.json',dict(unique_history_rows=len(facts),unique_ids=len(latest),
        latest_active=sum(f['status']=='active' for f in latest.values()),
        latest_active_expired=sum(f['status']=='active' and bool(f['expires']) and f['expires']<'2026-11-06' for f in latest.values()),
        sqlite_full_content_history_equal=True,baseline_repeats_identical=True))


def embed_all(args, out, latest, cases, resources):
    times=Journal(out/'embedding-times.jsonl')
    with stage('model-load-authenticate',resources):
        start=time.perf_counter();model=Embedder(args.model_cache);load_ms=(time.perf_counter()-start)*1000
    facts={}; queries={}; cache={}; started=time.monotonic()
    with stage('passage-backfill',resources):
        active=sorted((f for f in latest.values() if f['status']=='active'),key=lambda f:f['id'])
        for f in active:
            key=content_hash(f['content']);start=time.perf_counter();hit=key in cache
            if not hit:
                vec,ms=model.encode([f['content']],'passage: ');cache[key]=vec[0]
            else: ms=0.0
            facts[f['id']]=cache[key]
            times.write(dict(kind='passage',id=f['id'],cache_hit=hit,encode_ms=ms,full_ms=(time.perf_counter()-start)*1000,
                             input_characters=len(f['content']),truncated=len(model.tokenizer.encode('passage: '+f['content']).ids)>=512))
            if time.monotonic()-started>2400:raise TimeoutError('predeclared embedding bound exceeded')
    cache={}
    with stage('query-embedding',resources):
        for c in cases:
            key=content_hash(c['query']['Text']);hit=key in cache
            if not hit:vec,ms=model.encode([c['query']['Text']],'query: ');cache[key]=vec[0]
            else:ms=0.0
            queries[c['id']]=cache[key];times.write(dict(kind='query',id=c['id'],cache_hit=hit,encode_ms=ms))
        for rep in range(2):
            for c in cases:
                start=time.perf_counter();v=cache[content_hash(c['query']['Text'])];assert len(v)==384
                times.write(dict(kind='query-cache',id=c['id'],rep=rep,cache_ms=(time.perf_counter()-start)*1000))
    times.close()
    save(out/'embedding-provenance.json',dict(**model.provenance,tag=TAG,dimensions=384,load_ms=load_ms,
        provider_calls=0,provider_billing=None,local_operating_cost=None,threads=2,batch=1,
        onnxruntime=__import__('onnxruntime').__version__,tokenizers=__import__('tokenizers').__version__,numpy=__import__('numpy').__version__))
    save(out/'vectors.json',dict(facts=facts,queries=queries))
    save(out/'query-vectors.json',queries)
    return facts,queries


def query_cli(args):
    config=json.loads(Path(args.config).read_text());out=Path(config['evidence'])
    cases=json.loads((out/'cases.json').read_text());case=next(c for c in cases if c['id']==args.case)
    start=time.perf_counter();embed_ms=0.0;load_ms=0.0
    if args.arm.startswith('sqlite-'):
        reply=sqlite_query(config['bridge'],out/'sqlite'/(args.arm+'.db'),case['query'])
    else:
        vector=None
        if args.arm!='pg-lexical':
            if args.cold_model:
                t=time.perf_counter();model=Embedder(config['model_cache']);load_ms=(time.perf_counter()-t)*1000
                v,embed_ms=model.encode([case['query']['Text']],'query: ');vector=v[0]
            else:
                t=time.perf_counter();vector=json.loads((out/'query-vectors.json').read_text())[case['id']];embed_ms=(time.perf_counter()-t)*1000
        dep=Deployment(config['endpoint'],config['prefix'])
        with dep.connect() as db:
            norm=Normalizer(config['bridge']).query(case['query']) if args.fresh_normalization else config['norms'][case['id']]
            reply=dep.search(db,norm,case['query'],args.arm,vector)
    reply.update(embed_ms=embed_ms,model_load_ms=load_ms,client_ms=(time.perf_counter()-start)*1000)
    # Full research JSON response includes primary content; never auxiliary text.
    content=json.loads((out/'latest-content.json').read_text())
    for hit in reply['hits']:hit['content']=content[hit['id']]
    print(json.dumps(reply,ensure_ascii=False))


def run(args, out):
    started=time.monotonic();args.bridge=str(Path(args.bridge).absolute())
    manifest=verify_inputs(args.input)
    records=[json.loads(line) for line in (Path(args.input)/'search.log').read_text().splitlines() if line.strip()]
    cases=select_contexts(records,manifest['host'],'2026-11-06')
    frozen=freeze(args,out,cases,manifest)
    resources=Journal(out/'resources.jsonl')
    try:
        with stage('native-negative-controls',resources):negative_controls(args,out)
        with stage('native-parser',resources):
            invoke([args.bridge,'corpus',Path(args.input)/'facts',out/'cases.json',out/'native'],out,'native-parser')
        fixture=json.loads((out/'native/fixture.json').read_text());facts=fixture['facts'];latest=latest_facts(facts)
        with stage('sqlite-full-and-content-import-replay',resources):
            invoke([args.bridge,out/'native/fixture.json',out/'sqlite'],out,'sqlite-replay',timeout=600)
        histories(out,latest,facts)
        print('Native unique history, baseline repeats and field-isolation controls passed',flush=True)
        normalized=json.loads((out/'sqlite/normalized.json').read_text());norms={c['id']:c for c in normalized['cases']}
        save(out/'latest-content.json',{id:f['content'] for id,f in latest.items()})
        with stage('native-floor-diagnostic',resources):
            floor=[]
            for arm in ARMS[:2]:
                for c in cases[:16]:
                    q=dict(c['query'],MinScore=0.6)
                    floor.append(dict(arm=arm,case=c['id'],**sqlite_query(args.bridge,out/'sqlite'/(arm+'.db'),q)))
            save(out/'floor-results.json',floor)
        vectors,query_vectors=embed_all(args,out,latest,cases,resources)
        print('Genuine content-only passage and query embeddings saved',flush=True)
        dep=Deployment(json.loads(Path(args.endpoint).read_text()),args.prefix)
        expected=candidate_history(facts);expected.sort(key=lambda f:(f['id'],f['version']))
        with dep.connect() as db:
            save(out/'deployment-preflight.json',dep.preflight(db,require_absent=True))
            with stage('deployed-schema-history-index-import',resources):
                before=dep.create(db)
                dep.import_history(db,facts,latest,normalized['stems'])
            with stage('deployed-vector-import',resources):dep.import_vectors(db,latest,vectors,{id:content_hash(latest[id]['content']) for id in vectors})
            projection=db.execute(f'SELECT * FROM {dep.table("facts")} ORDER BY id,version').fetchall()
            save(out/'pg-history.json',projection);assert projection==expected,'history projection differs'
            columns=db.execute("SELECT column_name FROM information_schema.columns WHERE table_schema='research' AND table_name=%s",(args.prefix+'facts',)).fetchall()
            assert {r['column_name'] for r in columns}==set(COLUMNS)
            save(out/'database-current.json',dict(relations=dep.sizes(db),counts={s:db.execute(f'SELECT count(*) n FROM {dep.table(s)}').fetchone()['n'] for s in ('facts','fact_index','fact_fts','fact_vectors','stamps')},
                backend_memory=dep.backend_memory(db),
                settings=db.execute("SELECT name,setting FROM pg_settings WHERE name IN ('server_version','shared_buffers','work_mem','max_parallel_workers_per_gather','jit') ORDER BY name").fetchall()))
        ranks=Journal(out/'pg-results.jsonl');workloads=Journal(out/'workloads.jsonl')
        with stage('deployed-warm-replay',resources):
            for workers in (1,2):
                # Each task gets an authenticated session; connections are reused within worker chunks.
                for rep in range(2):
                    for arm in (ARMS[2:] if rep==0 else tuple(reversed(ARMS[2:]))):
                        start=time.monotonic()
                        def chunk(group):
                            result=[]
                            with dep.connect() as db:
                                for c in group:
                                    row=dep.search(db,norms[c['id']],c['query'],arm,query_vectors[c['id']])
                                    result.append(dict(arm=arm,case=c['id'],concurrency=workers,rep=rep,**row))
                            return result
                        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
                            for group in pool.map(chunk,[cases[i::workers] for i in range(workers)]):
                                for row in group:ranks.write(row)
                        workloads.write(dict(phase='warm-session',arm=arm,concurrency=workers,rep=rep,requests=len(cases),wall_s=time.monotonic()-start))
        ranks.close()
        config=dict(evidence=str(out),endpoint=dep.endpoint,prefix=args.prefix,bridge=args.bridge,model_cache=str(Path(args.model_cache).absolute()),norms=norms)
        save(out/'client-config.json',config)
        cli=Journal(out/'cli-results.jsonl')
        with stage('research-cli-contrasts',resources):
            for workers in (1,2):
                for arm in ARMS:
                    start=time.monotonic()
                    def one(c):
                        argv=[sys.executable,'-B',str(HERE/'private_replay.py'),'query','--config',str(out/'client-config.json'),'--case',c['id'],'--arm',arm]
                        t=time.perf_counter();p=subprocess.run(argv,capture_output=True,text=True,timeout=30)
                        if p.returncode:raise RuntimeError('research CLI failed: '+p.stderr)
                        reply=json.loads(p.stdout)
                        return dict(arm=arm,case=c['id'],concurrency=workers,phase='new-process-vector-cache',argv=argv,exit=p.returncode,
                            elapsed_ms=(time.perf_counter()-t)*1000,response_bytes=len(p.stdout.encode()),**reply)
                    with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
                        for row in pool.map(one,cases[:8]):cli.write(row)
                    workloads.write(dict(phase='research-cli',arm=arm,concurrency=workers,requests=8,wall_s=time.monotonic()-start))
            for arm in ARMS[3:]:
                for c in cases[:3]:
                    argv=[sys.executable,'-B',str(HERE/'private_replay.py'),'query','--config',str(out/'client-config.json'),'--case',c['id'],'--arm',arm,'--cold-model']
                    t=time.perf_counter();p=subprocess.run(argv,capture_output=True,text=True,timeout=30)
                    if p.returncode:raise RuntimeError('cold model CLI failed: '+p.stderr)
                    cli.write(dict(arm=arm,case=c['id'],concurrency=1,phase='new-process-model-load',argv=argv,exit=p.returncode,
                        elapsed_ms=(time.perf_counter()-t)*1000,response_bytes=len(p.stdout.encode()),**json.loads(p.stdout)))
        cli.close();print('Deployed retrieval, concurrency 1/2 and research CLI contrasts saved',flush=True)
        scale=Journal(out/'scale-results.jsonl');old=0
        for count in (10000,30000):
            with dep.connect() as db:
                with stage('scale-import-'+str(count),resources):dep.scale_load(db,old,count)
                save(out/('scale-size-'+str(count)+'.json'),dep.sizes(db))
            for workers in (1,2):
                for rep in range(3):
                    for arm in ('pg-lexical','pg-vector-exact'):
                        start=time.monotonic()
                        def scale_chunk(group):
                            result=[]
                            with dep.connect() as db:
                                for c in group:
                                    t=time.perf_counter();hits=dep.scale_search(db,norms[c['id']],query_vectors[c['id']],arm)
                                    result.append(dict(size=count,arm=arm,case=c['id'],concurrency=workers,rep=rep,sql_ms=(time.perf_counter()-t)*1000,hits=hits))
                            return result
                        with concurrent.futures.ThreadPoolExecutor(max_workers=workers) as pool:
                            for group in pool.map(scale_chunk,[cases[:8][i::workers] for i in range(workers)]):
                                for row in group:scale.write(row)
                        workloads.write(dict(phase='controlled-scale',size=count,arm=arm,concurrency=workers,rep=rep,requests=8,wall_s=time.monotonic()-start))
            old=count
        scale.close();workloads.close()
        with dep.connect() as db:
            projection_after=db.execute(f'SELECT * FROM {dep.table("facts")} ORDER BY id,version').fetchall()
            assert projection_after==projection,'queries changed history'
            after=dep.preflight(db)
            old_relations={r['relname']:r for r in before['existing_relations']}
            assert all(r in after['existing_relations'] for r in old_relations.values()),'pre-existing relation identity/ownership changed'
            counts={s:db.execute(f'SELECT count(*) n FROM {dep.table(s)}').fetchone()['n'] for s in ('facts','fact_index','fact_fts','fact_vectors','stamps','scale')}
            assert counts['fact_vectors']==len(vectors) and counts['scale']==30000
            save(out/'deployment-after.json',dict(preflight=after,relations=dep.sizes(db),counts=counts,history_unchanged=True,existing_relations_unchanged=True,
                vector_rows=db.execute(f'SELECT id,version,tag,dim,text_hash FROM {dep.table("fact_vectors")} ORDER BY id').fetchall()))
        verify_inputs(args.input)
        for name,h in frozen['source_hashes'].items():assert hashlib.sha256((HERE/name).read_bytes()).hexdigest()==h,'harness source changed'
        assert time.monotonic()-started<5400,'total declared bound exceeded'
        save(out/'complete.json',dict(status='private unlabeled replay executed; issue remains open',wall_s=time.monotonic()-started,
            source_unchanged=True,input_unchanged=True,provider_calls=0,provider_billing=None,server_cpu_rss=None,network_bytes=None))
    finally:resources.close()


def main():
    os.umask(0o077)
    parser=argparse.ArgumentParser();sub=parser.add_subparsers(dest='command',required=True)
    execute=sub.add_parser('run')
    for arg in ('input','bridge','model-cache','endpoint','pgpass','prefix','out'):execute.add_argument('--'+arg,required=True)
    query=sub.add_parser('query')
    for arg in ('config','case','arm'):query.add_argument('--'+arg,required=True)
    query.add_argument('--cold-model',action='store_true')
    query.add_argument('--fresh-normalization',action='store_true')
    args=parser.parse_args()
    if args.command=='query':query_cli(args);return
    credential=Path(args.pgpass).absolute()
    if credential.is_symlink() or credential.stat().st_mode&0o777!=0o600:raise ValueError('private password-file mode required')
    os.environ['PGPASSFILE']=str(credential)
    with new_output_directory(args.out) as out:run(args,out)


if __name__=='__main__':main()
