"""Bounded executable research driver. Every result is synthetic/local."""
import argparse,concurrent.futures,hashlib,http.client,json,os,platform,resource,secrets,socket,subprocess,sys,time
from pathlib import Path
from backend import COLUMNS,ContractError,canonical,connect,digest,insert_fact,publish_vector,reindex,search,mutate
from embed import Embedder,TAG,content_hash
from normalize import Normalizer
from stand import Stand
from api import service
from outputs import new_output_directory
from source_identity import verify_source,verify_bridge
HERE=Path(__file__).resolve().parent

def save(path,value):
    path.write_text(json.dumps(value,ensure_ascii=False,indent=2)+'\n');path.chmod(0o600)
def pg_history(db): return db.execute('SELECT * FROM facts ORDER BY id,version').fetchall()
def request_http(port,token,path,body,key=None):
    c=http.client.HTTPConnection('127.0.0.1',port,timeout=4)
    headers={'Authorization':'Bearer '+token,'Content-Type':'application/json'}
    if key is not None: headers['Idempotency-Key']=key
    try:
        c.request('POST',path,json.dumps(body),headers);r=c.getresponse();return r.status,json.loads(r.read())
    finally: c.close()

def failures(stand,dsn,bridge,fixture,vectors,out):
    norm=Normalizer(bridge);allowed=['repo:demo','host:client-a','global','shelf:research'];caller=dict(project=['demo'],host='client-a')
    evidence=[]
    def check(name,condition):
        evidence.append(dict(name=name,passed=bool(condition)));save(out/'failure-results.json',evidence)
        assert condition,name
    def request(op,id='txn-demo',**kw): return dict(op=op,id=id,caller=caller,now='2026-11-06T12:00:00Z',**kw)
    f={c:fixture['facts'][0][c] for c in COLUMNS};f.update(id='txn-demo',content='A synthetic transactional fact.',channel='behaviour')
    add=request('add',fact=f)
    token=secrets.token_urlsafe(32)
    with service(dsn,token,norm) as port:
        a=request_http(port,token,'/v1/mutate',add,'same-key');b=request_http(port,token,'/v1/mutate',add,'same-key')
        check('identical_retry_replays_reply',a==b and a[0]==200)
        different=dict(add,now='2026-11-06T12:00:01Z')
        bad=request_http(port,token,'/v1/mutate',different,'same-key');check('mismatched_payload_409',bad[0]==409 and bad[1]['error']['code']=='idempotency_payload_mismatch')
        # Send a real request and wait for durable commit, then discard its unread response.
        f2=dict(f,id='lost-response');lost=request('add',id='lost-response',fact=f2)
        s=socket.create_connection(('127.0.0.1',port));body=json.dumps(lost).encode()
        s.sendall(f'POST /v1/mutate HTTP/1.1\r\nHost: localhost\r\nAuthorization: Bearer {token}\r\nIdempotency-Key: lost-key\r\nContent-Length: {len(body)}\r\n\r\n'.encode()+body)
        with connect(dsn) as db:
            for _ in range(60):
                if db.execute("SELECT count(*) AS n FROM operations WHERE key='lost-key'").fetchone()['n']==1: break
                time.sleep(.02)
            else: raise AssertionError('lost-response fixture failed to commit')
        s.close()
        replay=request_http(port,token,'/v1/mutate',lost,'lost-key')
        with connect(dsn) as db: n=db.execute("SELECT count(*) AS n FROM facts WHERE id='lost-response'").fetchone()['n']
        check('committed_unread_response_retry_no_duplicate',replay[0]==200 and replay[1]['version']==1 and n==1)
        # Two real DB sessions contend on expected version 1.
        def competing(op):
            r=request(op,expected_version=1)
            if op=='correct': r['content']='A corrected synthetic transactional fact.'
            try:
                with connect(dsn) as db: return mutate(db,'fixture-client','race-'+op,r,norm.stems,allowed)
            except ContractError as e: return e.code
        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool: raced=list(pool.map(competing,['correct','retire']))
        check('concurrent_correct_retire_one_version_conflict',sum(isinstance(x,dict) for x in raced)==1 and raced.count('version_conflict')==1)
        with connect(dsn) as db:
            current=db.execute("SELECT * FROM facts WHERE id='txn-demo' ORDER BY version DESC LIMIT 1").fetchone()
            check('race_preserves_two_history_versions',len(db.execute("SELECT * FROM facts WHERE id='txn-demo'").fetchall())==2)
            check('stale_embedding_version_rejected',not publish_vector(db,'txn-demo',1,content_hash(f['content']),vectors['ticket-a']))
            check('wrong_hash_embedding_rejected',not publish_vector(db,'txn-demo',current['version'],'bad-hash',vectors['ticket-a']))
            # Explicitly test both current active publication and retirement invalidation regardless of race winner.
            if current['status']=='retired':
                mutate(db,'fixture-client','restore',request('restore',expected_version=current['version']),norm.stems,allowed);current=db.execute("SELECT * FROM facts WHERE id='txn-demo' ORDER BY version DESC LIMIT 1").fetchone()
            check('current_embedding_published',publish_vector(db,'txn-demo',current['version'],content_hash(current['content']),vectors['ticket-a']))
            mutate(db,'fixture-client','retire-after-embed',request('retire',expected_version=current['version']),norm.stems,allowed)
            check('retirement_removes_vector_and_stale_job_rejected',db.execute("SELECT count(*) AS n FROM fact_vectors WHERE id='txn-demo'").fetchone()['n']==0 and not publish_vector(db,'txn-demo',current['version'],content_hash(current['content']),vectors['ticket-a']))
        q=dict(fixture['cases'][0]['query'],Text='cache',In=['all'])
        status,reply=request_http(port,token,'/v1/search',dict(caller=caller,query=q))
        check('api_authorization_filters_before_limit',status==200 and not ({'other-host','other-project'} & {x['id'] for x in reply['hits']}))
        check('api_get_forbidden_history_hidden',request_http(port,token,'/v1/history',dict(caller=caller,id='other-project'))[0]==404)
        check('api_authentication_rejects_wrong_token',request_http(port,'wrong','/v1/search',dict(caller=caller,query=q))[0]==401)
        check('api_caller_identity_not_server_host',request_http(port,token,'/v1/search',dict(caller=dict(project=['demo'],host='server'),query=q))[0]==403)
        check('api_history_versions_readable',request_http(port,token,'/v1/history',dict(caller=caller,id='history'))[1]['facts'][1]['version']==2)
    with connect(dsn) as db:
        before=digest(pg_history(db));operations=digest(db.execute('SELECT * FROM operations ORDER BY principal,key').fetchall())
    stand.stop();stand.start()
    with connect(dsn) as db:
        check('restart_preserves_all_history',digest(pg_history(db))==before)
        check('restart_preserves_durable_idempotency',digest(db.execute('SELECT * FROM operations ORDER BY principal,key').fetchall())==operations and mutate(db,'fixture-client','same-key',add,norm.stems,allowed)==a[1])
    stand.dump_restore('mechanics','restored')
    with connect(dsn) as a_db,connect(stand.dsn('restored')) as b_db:
        for table,order in [('facts','id,version'),('operations','principal,key'),('fact_index','id'),('fact_fts','rid'),('fact_vectors','id,tag'),('stamps','name')]:
            # tsvector/vector textual representations compare exactly through psycopg.
            check('dump_restore_'+table,digest(a_db.execute(f'SELECT * FROM {table} ORDER BY {order}').fetchall())==digest(b_db.execute(f'SELECT * FROM {table} ORDER BY {order}').fetchall()))
    # A stopped local DB produces explicit failure rather than a fabricated successful write.
    stand.stop()
    try:
        with connect(dsn): pass
    except Exception: check('database_outage_explicit_connect_failure',True)
    else: check('database_outage_explicit_connect_failure',False)
    stand.start()
    return evidence

def transport(stand,dsn,bridge,fixture,out):
    token=secrets.token_urlsafe(32);normalizer=Normalizer(bridge);q=fixture['cases'][0]['query'];norm=normalizer.query(q)
    req=dict(caller=dict(project=['demo'],host='client-a'),query=q)
    samples=[]
    with service(dsn,token,normalizer) as port:
        config=dict(port=port,token=token,bridge=str(bridge),dsn=dsn)
        save(out/'client-config.json',config);save(out/'request.json',req)
        # Warm sessions: both transports retain their connection; normalization is included for both.
        for route in ['wire','http']:
            conn=connect(dsn) if route=='wire' else http.client.HTTPConnection('127.0.0.1',port,timeout=4)
            try:
                for rep in range(30):
                    start=time.perf_counter()
                    if route=='wire':
                        n=normalizer.query(q);reply=search(conn,n,q,'pg-lexical')
                    else:
                        conn.request('POST','/v1/search',json.dumps(req),{'Authorization':'Bearer '+token});r=conn.getresponse();reply=json.loads(r.read());assert r.status==200
                    samples.append(dict(route=route,phase='warm-session',rep=rep,end_to_end_ms=(time.perf_counter()-start)*1000,sql_ms=reply['sql_ms'],embed_ms=0.0,hits=reply['hits']))
            finally: conn.close()
        # Cold invocations include Python startup/imports + CLI JSON + connect + normalized search.
        for route in ['wire','http']:
            for rep in range(10):
                start=time.perf_counter()
                p=subprocess.run([sys.executable,str(HERE/'client.py'),str(out/'client-config.json'),str(out/'request.json'),route],capture_output=True,text=True,timeout=8,check=True)
                reply=json.loads(p.stdout)
                samples.append(dict(route=route,phase='cold-cli',rep=rep,end_to_end_ms=(time.perf_counter()-start)*1000,client_ms=reply['client_ms'],sql_ms=reply['sql_ms'],embed_ms=0.0,hits=reply['hits']))
        (out/'client-config.json').unlink() # ephemeral bearer and DB password do not belong in result evidence
    save(out/'transport-results.json',samples)
    assert len({canonical(x['hits']) for x in samples})==1,'wire/http rankings differ'
    return samples

def main():
    parser=argparse.ArgumentParser();parser.add_argument('--pgroot',required=True);parser.add_argument('--model-cache',required=True);parser.add_argument('--bridge',required=True);parser.add_argument('--out',required=True);args=parser.parse_args()
    os.umask(0o077)
    with new_output_directory(args.out) as out:
        run(args,out)

def run(args,out):
    bridge=Path(args.bridge).resolve()
    fixture=json.loads((HERE/'fixture.json').read_text());declaration=json.loads((HERE/'declaration.json').read_text())
    identity=verify_source(HERE.parents[3],declaration['source'])
    native=verify_bridge(bridge,identity,HERE/'sqlite_bridge.go')
    frozen=dict(source=identity['evaluated_revision'],source_identity=identity,native_bridge=native,inputs={p.name:hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(HERE.iterdir()) if p.is_file()},bridge_sha256=hashlib.sha256(bridge.read_bytes()).hexdigest(),started_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime()))
    save(out/'freeze.json',frozen)
    commands=[]
    def run(argv):
        start=time.perf_counter();p=subprocess.run(argv,capture_output=True,text=True)
        commands.append(dict(argv=argv,exit=p.returncode,elapsed_ms=(time.perf_counter()-start)*1000));save(out/'commands.json',commands)
        save(out/'sqlite-driver-output.json',dict(stdout=p.stdout,stderr=p.stderr))
        if p.returncode: raise RuntimeError('SQLite driver failed; see private driver output')
    run([str(bridge),str(HERE/'fixture.json'),str(out/'sqlite')])
    normalized=json.loads((out/'sqlite/normalized.json').read_text());norms={x['id']:x for x in normalized['cases']}
    print('SQLite baseline controls executed',flush=True)
    # Authenticate model bytes before producing vectors. At most two input texts per inference.
    start=time.perf_counter();embedder=Embedder(args.model_cache);load_ms=(time.perf_counter()-start)*1000
    latest={f['id']:f for f in fixture['facts']};vectors={};embedding_samples=[];embedding_start=time.monotonic()
    for id,f in latest.items():
        v,ms=embedder.encode([f['content']],'passage: ');vectors[id]=v[0];embedding_samples.append(dict(kind='passage',id=id,ms=ms,tokens_source='not billed'))
        if time.monotonic()-embedding_start>600: raise TimeoutError('embedding budget exceeded')
    query_vectors={}
    for c in fixture['cases']:
        v,ms=embedder.encode([c['query']['Text']],'query: ');query_vectors[c['id']]=v[0];embedding_samples.append(dict(kind='query',id=c['id'],ms=ms))
        if time.monotonic()-embedding_start>600: raise TimeoutError('embedding budget exceeded')
    save(out/'embedding-provenance.json',dict(**embedder.provenance,tag=TAG,dimensions=384,load_ms=load_ms,provider_usage=None,provider_cost=None,measured_provider_calls=0,model_download_bytes=sum(x['bytes'] for x in embedder.provenance['files'])))
    save(out/'vectors.json',dict(facts=vectors,queries=query_vectors));save(out/'embedding-times.json',embedding_samples)
    print('Genuine multilingual embeddings computed',flush=True)
    stand=Stand(args.pgroot,out)
    try:
        stand.init();stand.start()
        with connect(stand.dsn('postgres')) as db: db.execute('CREATE DATABASE research')
        dsn=stand.dsn()
        with connect(dsn) as db:
            db.execute((HERE/'schema.sql').read_text())
            for f in fixture['facts']: insert_fact(db,f)
            for id,f in latest.items():
                reindex(db,f,normalized['stems'][id])
                if f['status']=='active': assert publish_vector(db,id,f['version'],content_hash(f['content']),vectors[id])
            db.execute('INSERT INTO stamps VALUES (%s,%s)',('source-snapshot',digest(fixture['facts'])))
            expected=sorted([{c:f[c] for c in COLUMNS} for f in fixture['facts']],key=lambda f:(f['id'],f['version']))
            assert pg_history(db)==expected,'PG field/history mapping changed facts'
            assert not {'keywords','queries'} & {x['column_name'] for x in db.execute("SELECT column_name FROM information_schema.columns WHERE table_name='facts'").fetchall()}
            before=digest(pg_history(db));results=[]
            for rep in range(2):
                for arm in ['pg-lexical','pg-vector-exact','pg-hybrid-rrf60']:
                    for c in fixture['cases']:
                        reply=search(db,norms[c['id']],c['query'],arm,query_vectors[c['id']])
                        results.append(dict(arm=arm,case=c['id'],rep=rep,**reply))
            save(out/'pg-results.json',results)
            assert digest(pg_history(db))==before,'retrieval changed history'
            save(out/'pg-history.json',pg_history(db))
            save(out/'database-identity.json',dict(server=db.execute('SELECT version() AS version').fetchone(),extension=db.execute("SELECT extversion FROM pg_extension WHERE extname='vector'").fetchone(),size=db.execute('SELECT pg_database_size(current_database()) AS bytes').fetchone(),index_sizes=db.execute("SELECT relname,pg_total_relation_size(oid) AS bytes FROM pg_class WHERE relname IN ('facts','fact_index','fact_fts','fact_vectors','stamps','operations') ORDER BY relname").fetchall()))
        transport(stand,dsn,bridge,fixture,out)
        print('Retrieval arms and actual local wire/API/CLI roundtrips executed',flush=True)
        with connect(stand.dsn('postgres')) as db: db.execute('CREATE DATABASE mechanics TEMPLATE research')
        failures(stand,stand.dsn('mechanics'),bridge,fixture,vectors,out)
        save(out/'integrity.json',dict(pg_remaining_fields_equal_frozen_history=True,pg_history_unchanged_after_retrieval=True,source_snapshot_sha256=hashlib.sha256((HERE/'fixture.json').read_bytes()).hexdigest(),sqlite_history_equal=json.loads((out/'sqlite/sqlite-full-history.json').read_text())==json.loads((out/'sqlite/sqlite-content-history.json').read_text())))
        print('Failure/restart/dump-restore fixtures executed',flush=True)
    finally:
        stand.stop()
        for p in out.rglob('*'):
            if p.is_file(): p.chmod(0o600)
            elif p.is_dir(): p.chmod(0o700)
    save(out/'driver-resources.json',dict(driver_peak_rss_kib=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,driver_cpu_user_s=resource.getrusage(resource.RUSAGE_SELF).ru_utime,driver_cpu_system_s=resource.getrusage(resource.RUSAGE_SELF).ru_stime,python=sys.version,platform=platform.machine()))
    # Detect source/input edits during evaluation.
    assert all(hashlib.sha256((HERE/name).read_bytes()).hexdigest()==h for name,h in frozen['inputs'].items()),'evaluated sources changed during run'
    save(out/'complete.json',dict(status='checkpoint executed; issue acceptance remains incomplete',source_hashes_unchanged=True,finished_utc=time.strftime('%Y-%m-%dT%H:%M:%SZ',time.gmtime())))
if __name__=='__main__': main()
