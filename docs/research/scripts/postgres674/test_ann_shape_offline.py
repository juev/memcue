"""Offline regression controls; SQLite memory, no libpq or private input reads.

The SQL adapter supports ONLY these homogeneous identical unit-vector fixtures:
cosine distance is zero, FETCH WITH TIES means every eligible fixture row,
numbered bindings retain positions, and julianday parses the timestamp offsets.
It is not a PostgreSQL, heterogeneous-vector, ANN, or full-corpus oracle.
"""
import contextlib
import io
import json
import os
from pathlib import Path
import re
import sqlite3
import tempfile
import unittest
from unittest.mock import patch

import ann_shape_study as study
import test_ann_shape as fixtures


class SQLiteFixtureDB:
    def __init__(self):
        self.db=sqlite3.connect(':memory:');self.db.row_factory=sqlite3.Row
        self.db.create_function('cosine',2,lambda a,b:0.0)
        self.db.create_function('to_tsvector',2,lambda config,v:v)
        self.db.create_function('to_tsquery',2,lambda config,v:v)
        self.db.create_function('lexicalmatch',2,lambda a,b:a==b)

    def execute(self,sql,params,prepare=False):
        bindings=iter(range(1,len(params)+1))
        sql=re.sub(r'%s',lambda m:'?'+str(next(bindings)),sql)
        sql=sql.replace('::public.vector','').replace('f.created::timestamptz','julianday(f.created)')
        sql=re.sub(r'v.vec <=> (\?\d+)',r'cosine(v.vec,\1)',sql)
        sql=re.sub(r' FETCH FIRST \?\d+ ROWS WITH TIES','',sql)
        sql=sql.replace(' OFFSET 0)',' LIMIT -1 OFFSET 0)')
        sql=re.sub(r'(f\.\w+) = ANY\((\?\d+)\)',r'\1 IN (SELECT value FROM json_each(\2))',sql)
        sql=re.sub(r'lower\(f.scope\)=ANY\((\?\d+)\)',r'lower(f.scope) IN (SELECT value FROM json_each(\1))',sql)
        sql=re.sub(r"x.lexemes @@ to_tsquery\('simple',(\?\d+)\)",r"lexicalmatch(x.lexemes,to_tsquery('simple',\1))",sql)
        sql=re.sub(r'left\(([^,]+),(\d+|\?\d+)\)',r'substr(\1,1,\2)',sql)
        values=[json.dumps(x) if isinstance(x,list) else x for x in params]
        rows=[dict(r) for r in self.db.execute(sql,values).fetchall()]
        class Cursor:
            def fetchall(self):return rows
        return Cursor()


class FixtureOps:
    def execute(self,db,q,p,**kw):return db.execute(q,p).fetchall(),0.0


class OfflineRegressions(unittest.TestCase):
    def test_eligibility_guards_are_observable_above_topk(self):
        original=fixtures.candidate
        with patch.object(study.psycopg,'connect',side_effect=AssertionError('network forbidden')):
            for name,old,new in [
                ('baseline',None,None),
                ('latest','f.version=(SELECT max(version) FROM facts z WHERE z.id=f.id)','true'),
                ('status',"f.status='active'",'true'),
                ('expiry',"(f.expires='' OR f.expires>=%s)",'(%s IS NOT NULL)')]:
                with self.subTest(guard=name):
                    def compiler(*a,**kw):
                        q,p=original(*a,**kw)
                        if old:
                            self.assertEqual(q.count(old),1)
                            q=q.replace(old,new)
                        return q,p
                    db=SQLiteFixtureDB()
                    try:
                        with patch.object(fixtures,'candidate',compiler),patch.object(fixtures,'LIVE',(db,FixtureOps())):
                            result=unittest.TextTestRunner(stream=io.StringIO()).run(fixtures.live_suite())
                        self.assertEqual(len(result.errors),0)
                        self.assertEqual(result.testsRun,6)
                        self.assertEqual(result.wasSuccessful(),name=='baseline')
                    finally:db.db.close()

    def test_actual_stage_retains_completed_hits_after_later_timing_failure(self):
        case=dict(id='synthetic',query=dict(Host='test',Project=[]))
        truth=[dict(id='synthetic-fact',version=1,score=1.0)]
        data=dict(endpoint=dict(host='synthetic',port=1,dbname='synthetic',user='synthetic',sslmode='disable'),
                  cases=[case],selected=[case],norms={'synthetic':dict(filter={},pg_query='')},
                  vectors={'synthetic':[1.0]+[0.0]*383},
                  oracles={s:{'synthetic':truth} for s in study.PREFIXES},
                  eligible={s:{'synthetic':{('synthetic-fact',1)}} for s in study.PREFIXES})
        base=study.Operations
        class InjectedOperations(base):
            completed_timed=0
            def execute(self,db,q,p=(),**kw):
                if kw['phase']=='timing':
                    if self.completed_timed==1:raise RuntimeError('injected later timing failure')
                    self.completed_timed+=1
                values=([{'QUERY PLAN':[{'Plan':{'Node Type':'Index Scan','Index Name':study.INDEX,'Actual Rows':1}}]}]
                        if kw['phase'] in ('coverage','diagnostic') else truth)
                class DB:
                    def execute(self,*a,**k):
                        class Cursor:
                            def fetchall(self):return values
                        return Cursor()
                return super().execute(DB(),q,p,**kw)
        @contextlib.contextmanager
        def connection(*a,**kw):yield object()
        def saved(path,names):
            if path.name=='candidate-freeze.json':return {'source_hashes':study.identities()}
            return {'status':'GREEN' if 'test-green' in str(path) else 'METADATA_VERIFIED'}
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            owned=Path(tmp)/'packet';owned.mkdir(mode=0o700)
            with patch.multiple(study,OWN=owned,inputs=lambda:data,read=saved,connect=connection,Operations=InjectedOperations),\
                 patch.object(study.psycopg,'connect',side_effect=AssertionError('network forbidden')),\
                 contextlib.redirect_stdout(io.StringIO()):
                self.assertEqual(study.stage('run'),1)
            packet=owned/'run'
            result=json.loads((packet/'result.json').read_text())
            self.assertEqual(result['status'],'FAILED')
            # The actual driver must save prior successes even when a later request fails.
            self.assertTrue((packet/'timings.json').exists())
            self.assertTrue((packet/'warmups.json').exists())
            timings=json.loads((packet/'timings.json').read_text())
            self.assertEqual(len(timings),1)
            self.assertEqual(timings[0]['hits'],truth)
            self.assertEqual(timings[0]['quality']['strict_id_version_recall_at10'],1)
            self.assertEqual(result['timed_sql'],1)
            operations=[json.loads(x) for x in (packet/'operations.jsonl').read_text().splitlines()]
            self.assertEqual(next(x for x in operations if x['phase']=='timing')['result_rows'],truth)

if __name__=='__main__':unittest.main()
