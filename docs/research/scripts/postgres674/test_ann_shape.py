"""Synthetic SQL tests: VALUES only, no CREATE and no private text/IDs."""
import json
import os
from pathlib import Path
import tempfile
import unittest

from ann_shape import (SPACE, Operations, agreement, candidate, reference, save,
                       validate_vector)
from embed import TAG, literal
from outputs import create_output_directory

VECTOR = [1.0]+[0.0]*383
QUERY = dict(Host='test-host', Project=['test-project'])


class Tables:
    def table(self, suffix):
        return suffix


class Helpers(unittest.TestCase):
    def test_invalid_vector_and_space_through_compiler(self):
        for vector, space in [(VECTOR[:-1],SPACE),(VECTOR+[0],SPACE),([float('nan')]+VECTOR[1:],SPACE),
                              ([0.0]*384,SPACE),(VECTOR,'inner-product')]:
            with self.subTest(space=space,dimensions=len(vector)):
                with self.assertRaises(ValueError):
                    candidate(Tables(),dict(filter={},pg_query=''),QUERY,vector,space=space)

    def test_filter_template_retains_allterms_and_parsed_ties(self):
        sql, params = candidate(Tables(),dict(filter={'AllTerms':True,'Today':'2026-01-01'},pg_query='anchor'),QUERY,VECTOR)
        self.assertIn('SELECT max(version)',sql)
        self.assertIn('OFFSET 0',sql)
        self.assertIn('x.lexemes @@',sql)
        self.assertIn('f.created::timestamptz DESC',sql)
        self.assertIn('anchor',params)
        self.assertNotIn('anchor',sql)

    def test_new_outputs_refuse_collisions_and_links(self):
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as d:
            root=Path(d);out=create_output_directory(root/'owned')
            save(out/'record.json',{'value':1})
            with self.assertRaises(FileExistsError):create_output_directory(out)
            with self.assertRaises(FileExistsError):save(out/'record.json',{'value':2})
            self.assertEqual(json.loads((out/'record.json').read_text()),{'value':1})
            (root/'alias').symlink_to(out,target_is_directory=True)
            with self.assertRaises(FileExistsError):save(root/'alias'/'new.json',{})
            (out/'link.json').symlink_to(out/'record.json')
            with self.assertRaises(FileExistsError):save(out/'link.json',{})
            os.link(out/'record.json',out/'hard.json')
            with self.assertRaises(FileExistsError):save(out/'hard.json',{})
            self.assertEqual(out.stat().st_mode&0o777,0o700)
            self.assertEqual((out/'record.json').stat().st_mode&0o777,0o600)

    def test_sql_accounting_failure_and_skip(self):
        class DB:
            def execute(self,*args,**kw):raise RuntimeError('synthetic')
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as d:
            op=Operations(Path(d)/'ops.jsonl',1,10)
            with self.assertRaises(RuntimeError):op.execute(DB(),'SELECT 1',phase='test',label=0)
            op.skip('test',1)
            with self.assertRaises(TimeoutError):op.execute(DB(),'SELECT 1',phase='test',label=2)
            op.close();rows=[json.loads(x) for x in op.path.read_text().splitlines()]
            self.assertTrue(rows[0]['executed_sql']);self.assertFalse(rows[0]['success'])
            self.assertIsNone(rows[0]['sql_ms']);self.assertIsNone(rows[1]['sql_ms'])
            self.assertFalse(rows[1]['executed_sql'])

    def test_version_recall_boundary_and_underfill_are_distinct(self):
        truth=[dict(id='a',version=2,score=.5)]
        wrong=[dict(id='a',version=1,score=.5)]
        v=agreement(wrong,truth,{('a',2)})
        self.assertEqual(v['strict_id_version_recall_at10'],0)
        self.assertEqual(v['boundary_valid_fraction'],1)
        self.assertFalse(v['eligible']);self.assertFalse(v['exact_equal'])
        self.assertEqual(agreement([],truth,set())['underfill'],1)


# The live test runner injects one bounded, read-only connection and operation journal.
LIVE = None


def fixture():
    # Deliberately adverse vector storage order; 12 ties cross k=10.
    rows=[]
    for i in range(12):
        rows.append((f'tie-{11-i:02}',1,'active','','global','rule','test-project note',
                     '2026-01-01T00:00:00Z',True,True))
    # Ineligible sentinels outrank the 12 eligible ties if a guard is lost.
    rows += [('retired',1,'active','','host:test-host','rule','','2026-01-01T00:00:00Z',True,True),
             ('retired',2,'retired','','global','rule','','2026-01-02T00:00:00Z',False,False),
             ('expired',1,'active','','host:test-host','rule','','2026-01-01T00:00:00Z',True,True),
             ('expired',2,'active','2020-01-01','global','rule','','2026-01-02T00:00:00Z',False,False),
             ('stale',1,'active','','host:test-host','rule','','2026-01-01T00:00:00Z',True,True),
             ('stale',2,'active','','repo:test-project','note','','2026-01-02T00:00:00Z',False,False),
             ('fresh',1,'active','','repo:test-project','note','shelf x','2026-01-03T00:00:00+02:00',True,True),
             ('host',1,'active','','host:test-host','note','','2026-01-02T23:30:00Z',True,True),
             ('old',1,'active','','repo:archive','note','','2019-01-01T00:00:00Z',True,False),
             ('retired-current',1,'retired','','host:test-host','rule','','2026-01-04T00:00:00Z',True,True),
             ('expired-current',1,'active','2020-01-01','host:test-host','rule','','2026-01-04T00:00:00Z',True,True)]
    from psycopg.sql import Literal
    lit=lambda x:Literal(x).as_string()
    facts=','.join('('+','.join(lit(v) for v in row[:8])+')' for row in rows)
    index=[];fts=[];vectors=[]
    for n,row in enumerate(rows):
        if row[8]:
            index.append('('+','.join(lit(x) for x in [n,row[0],row[1]])+')')
            fts.append(f"({n},to_tsvector('simple',"+lit('anchor' if row[9] else 'other')+'))')
            vectors.append('('+','.join(lit(x) for x in [row[0],row[1],TAG])+','+lit(literal(VECTOR))+'::public.vector)')
    return ('WITH facts(id,version,status,expires,scope,kind,origin,created) AS (VALUES '+facts+'), '
            'fact_index(rid,id,version) AS (VALUES '+','.join(index)+'), '
            'fact_fts(rid,lexemes) AS (VALUES '+','.join(fts)+'), '
            'fact_vectors(id,version,tag,vec) AS (VALUES '+','.join(vectors)+') ')


@unittest.skipIf(LIVE is None, 'run via ann_shape_study test for bounded real SQL')
class LiveSemantics(unittest.TestCase):
    def check(self, filt, expected=None, limit=10):
        db,op=LIVE
        norm=dict(filter={'Today':'2026-11-07',**filt},pg_query='anchor')
        pair=[]
        for arm,compiler in [('reference',reference),('candidate',candidate)]:
            q,p=compiler(Tables(),norm,QUERY,VECTOR,limit)
            pair.append(op.execute(db,fixture()+q,p,phase='synthetic',label=self.id()+':'+arm)[0])
        # Both queries use production helper paths and PostgreSQL timestamp/vector operators.
        self.assertEqual(pair[0],pair[1])
        if expected is not None:self.assertEqual([(x['id'],x['version']) for x in pair[1]],expected)
        return pair[1]

    def test_ties_cross_limit_and_are_stable(self):
        r=self.check({'Scopes':['global'],'Kinds':['rule']})
        self.assertEqual([x['id'] for x in r],[f'tie-{i:02}' for i in range(10)])

    def test_latest_before_status_expiry_and_stale_index(self):
        r=self.check({},limit=10)
        self.assertTrue(all(x['id'] not in {'retired','expired','stale'} for x in r))

    def test_filter_before_limit_scope_kind_date(self):
        self.check({'Scopes':['repo:test-project'],'Kinds':['note'],'Since':'2026-01-03'},[('fresh',1)])

    def test_shelf_origin_and_excluded_scope(self):
        self.check({'Origins':['shelf'],'ExcludePrefixes':['repo:'],'KeepScopes':['repo:test-project']},[('fresh',1)])

    def test_allterms_before_limit(self):
        self.check({'AllTerms':True,'Scopes':['repo:archive']},[])

    def test_timestamp_priority_and_prefix(self):
        self.check({'ScopePrefixes':['repo:'],'Kinds':['note'],'ExcludePrefixes':['repo:archive']},[('fresh',1)])


def live_suite():
    # Decorator is evaluated at import; explicitly enable only for injected live runner.
    LiveSemantics.__unittest_skip__=False
    return unittest.TestLoader().loadTestsFromTestCase(LiveSemantics)

if __name__=='__main__':unittest.main()
