"""Post-run integrity audit of the actual saved dump; no quality tuning."""
import argparse,hashlib,json,os,subprocess,time
from pathlib import Path
from backend import COLUMNS,connect,canonical,mutate
from embed import TAG,content_hash
from normalize import Normalizer
from stand import Stand
from outputs import create_output_directory
HERE=Path(__file__).resolve().parent
if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('--pgroot',required=True);parser.add_argument('--evidence',required=True);parser.add_argument('--bridge',required=True);parser.add_argument('--out',required=True);a=parser.parse_args()
    os.umask(0o077);out=create_output_directory(a.out);evidence=Path(a.evidence).resolve()
    # Declare the audit before execution; this cannot alter measured ranks or labels.
    declaration=dict(purpose='post-run integrity only; independently read the saved dump, verify content-only index payloads and restore writability',driver_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),dump_sha256=hashlib.sha256((evidence/'backup.dump').read_bytes()).hexdigest(),threshold='all assertions pass',quality_parameters_changed=False,concurrency=1)
    (out/'declaration.json').write_text(json.dumps(declaration,indent=2))
    fixture=json.loads((HERE/'fixture.json').read_text());latest={f['id']:f for f in fixture['facts']};vectors=json.loads((evidence/'vectors.json').read_text())['facts'];normalizer=Normalizer(a.bridge);checks=[]
    def check(name,condition):
        checks.append(dict(name=name,passed=bool(condition)));(out/'assertions.json').write_text(json.dumps(checks,indent=2));assert condition,name
    stand=Stand(a.pgroot,out)
    try:
        stand.init();stand.start()
        with connect(stand.dsn('postgres')) as db: db.execute('CREATE DATABASE audit')
        env=dict(stand.env,PGHOST='127.0.0.1',PGPORT=str(stand.port),PGUSER='research',PGPASSWORD=stand.password)
        p=subprocess.run([str(stand.bin/'pg_restore'),'--exit-on-error','-d','audit',str(evidence/'backup.dump')],env=env,capture_output=True)
        check('saved_dump_restores_again',p.returncode==0)
        with connect(stand.dsn('audit')) as db:
            indexed={r['id']:r for r in db.execute('SELECT * FROM fact_index').fetchall()}
            stored={r['id']:r for r in db.execute('SELECT id,tag,dim,text_hash,version,vec::text AS vec FROM fact_vectors').fetchall()}
            for id,f in latest.items():
                rows=db.execute('SELECT * FROM facts WHERE id=%s ORDER BY version',(id,)).fetchall()
                check('frozen_history_'+id,rows==[{c:r[c] for c in COLUMNS} for r in fixture['facts'] if r['id']==id])
                check('index_eligibility_'+id,(id in indexed)==(f['status']=='active'))
                if f['status']=='active':
                    check('primary_content_stems_'+id,indexed[id]['stems']==normalizer.stems(f['content']) and indexed[id]['version']==f['version'])
                    v=stored[id]
                    check('primary_content_vector_'+id,v['text_hash']==content_hash(f['content']) and v['tag']==TAG and v['dim']==384 and v['version']==f['version'] and max(abs(x-y) for x,y in zip(json.loads(v['vec']),vectors[id]))<1e-7)
            for marker in ['quartzmarker','hiddenquerymarker','metadatamarker','evidencemarker','channelmarker']:
                check('fts_marker_absent_'+marker,db.execute("SELECT count(*) AS n FROM fact_fts WHERE lexemes @@ plainto_tsquery('simple',%s)",(marker,)).fetchone()['n']==0)
            f={c:fixture['facts'][0][c] for c in COLUMNS};f.update(id='restored-write-fixture',version=1)
            req=dict(op='add',id=f['id'],fact=f,caller=dict(project=['demo'],host='client-a'),now='2026-11-06T13:00:00Z')
            result=mutate(db,'fixture-client','restored-write',req,normalizer.stems,['repo:demo'])
            check('restore_is_writable_with_identity_sequence',result['version']==1 and db.execute('SELECT count(*) AS n FROM fact_index WHERE id=%s',(f['id'],)).fetchone()['n']==1)
            try:
                with db.transaction():
                    db.execute("SET LOCAL statement_timeout='10ms'")
                    db.execute('SELECT pg_sleep(0.2)')
            except Exception as e:
                import psycopg
                check('real_statement_deadline_cancels_sql',isinstance(e,psycopg.errors.QueryCanceled))
            else: check('real_statement_deadline_cancels_sql',False)
            check('connection_usable_after_deadline_rollback',db.execute('SELECT 1 AS n').fetchone()['n']==1)
        summary=dict(synthetic=True,post_run_integrity_audit=True,assertions=len(checks),passed=all(c['passed'] for c in checks),quality_parameters_changed=False,declaration=declaration)
        (out/'summary.json').write_text(json.dumps(summary,indent=2)+'\n')
        print(json.dumps(summary))
    finally: stand.stop()
