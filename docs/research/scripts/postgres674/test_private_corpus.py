import unittest
from private_corpus import candidate_history, latest_facts, select_contexts


class PrivateCorpus(unittest.TestCase):
    def test_stable_balanced_contexts_and_explicit_caller(self):
        rows = [dict(query=q, tier='example/project', host='caller') for q in ('hello', 'other', 'привет', 'ещё', '123')]
        a = select_contexts(rows, 'fallback', '2026-11-06', dict(latin=1,cyrillic=1,other=1))
        self.assertEqual(a, select_contexts(list(reversed(rows))+rows, 'fallback', '2026-11-06', dict(latin=1,cyrillic=1,other=1)))
        self.assertEqual({r['language'] for r in a}, {'latin','cyrillic','other'})
        self.assertTrue(all(r['query']['Host']=='caller' and r['query']['Project']==['example/project'] and r['query']['MinScore']==0 for r in a))
        with self.assertRaisesRegex(ValueError,'insufficient'):
            select_contexts(rows,'fallback','2026-11-06')

    def test_latest_is_selected_before_status_and_expiry(self):
        rows = [dict(id='x',version=2,status='retired',expires=''),dict(id='x',version=1,status='active',expires='')]
        self.assertEqual(latest_facts(rows)['x']['version'],2)
        self.assertEqual(latest_facts(rows)['x']['status'],'retired')
        self.assertEqual(candidate_history([dict(id='x',keywords='hidden',queries='hidden',content='primary',channel='old')]),[dict(id='x',content='primary',channel='old')])

    def test_deployed_prefix_and_endpoint_do_not_accept_credentials(self):
        from deployed import Deployment
        endpoint=dict(host='private.invalid',port=55432,dbname='research',user='research',sslmode='disable')
        with self.assertRaises(ValueError):Deployment(dict(endpoint,password='forbidden'),'p2_test_')
        with self.assertRaises(ValueError):Deployment(endpoint,'x;DROP TABLE')
        dep=Deployment(endpoint,'p2_test_')
        self.assertEqual(dep.table('facts'),'"research"."p2_test_facts"')
        with self.assertRaises(ValueError):dep.table('deployment_acceptance')

    def test_deployed_preflight_never_installs_extension_and_refuses_existing(self):
        from deployed import Deployment
        class Result:
            def __init__(self,value):self.value=value
            def fetchone(self):return self.value
            def fetchall(self):return self.value
        class DB:
            def __init__(self,existing,extension):self.existing=existing;self.extension=extension;self.statements=[]
            def execute(self,query):
                self.statements.append(query)
                if 'pg_extension' in query:return Result(self.extension)
                if 'pg_class' in query:return Result(self.existing)
                return Result({})
        endpoint=dict(host='private.invalid',port=55432,dbname='research',user='research',sslmode='disable')
        dep=Deployment(endpoint,'p2_test_')
        db=DB([dict(relname='p2_test_facts')],dict(extversion='0.8.7',nspname='public'))
        with self.assertRaises(FileExistsError):dep.preflight(db,True)
        self.assertFalse(any('CREATE' in q for q in db.statements))
        db=DB([],None)
        with self.assertRaises(ValueError):dep.preflight(db,True)
        self.assertFalse(any('CREATE' in q for q in db.statements))

    def test_optional_memory_permission_failure_is_unknown(self):
        import psycopg
        from deployed import Deployment
        class DB:
            def execute(self,query):raise psycopg.errors.InsufficientPrivilege('denied')
        endpoint=dict(host='private.invalid',port=55432,dbname='research',user='research',sslmode='disable')
        result=Deployment(endpoint,'p2_test_').backend_memory(DB())
        self.assertEqual(result['status'],'unavailable-app-role')
        self.assertIsNone(result['total_bytes'])

    def test_controlled_scale_bounds_have_unambiguous_integer_types(self):
        from deployed import Deployment
        class DB:
            def __init__(self):self.calls=[]
            def execute(self,*args):self.calls.append(args)
        endpoint=dict(host='private.invalid',port=55432,dbname='research',user='research',sslmode='disable')
        db=DB();Deployment(endpoint,'p2_test_').scale_load(db,0,10000)
        inserts=[q for q in db.calls if isinstance(q[0],str) and 'generate_series' in q[0]]
        self.assertEqual(len(inserts),1)
        self.assertIn('generate_series(%s::integer,%s::integer)',inserts[0][0])
        self.assertEqual(inserts[0][1],(1,10000))

    def test_private_run_rejects_existing_output_without_logging(self):
        import os
        from pathlib import Path
        import subprocess
        import sys
        import tempfile
        from test_output_safety import snapshot
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as temp:
            root=Path(temp);output=root/'prior';output.mkdir()
            victim=output/'failure-traceback.txt';victim.write_text('retained failure')
            before=snapshot(victim)
            credential=root/'synthetic-password-file';credential.write_text('synthetic fixture');credential.chmod(0o600)
            argv=[sys.executable,'-B',str(Path(__file__).with_name('private_replay.py')),'run']
            for option in ('input','bridge','model-cache','endpoint'):argv+=['--'+option,str(root/'absent')]
            argv+=['--pgpass',str(credential),'--prefix','p2_test_','--out',str(output)]
            result=subprocess.run(argv,capture_output=True,text=True,timeout=15)
            self.assertNotEqual(result.returncode,0)
            self.assertIn('FileExistsError',result.stderr)
            self.assertEqual(snapshot(victim),before)
            self.assertEqual([p.name for p in output.iterdir()],['failure-traceback.txt'])
