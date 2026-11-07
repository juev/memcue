"""Focused actual-helper contract tests; no network, models or private input."""
import json
import os
from pathlib import Path
import tempfile
import unittest

from ann_shape import candidate, reference
from ann_shape_exact import WholeBudget,DeadlineExceeded,Operations,compile_request
from test_ann_shape import Tables,VECTOR,QUERY


class ExactContracts(unittest.TestCase):
    def test_actual_correctness_dispatch_breaks_only_inner_index_order(self):
        norm=dict(filter={'Today':'2026-11-07','Scopes':['global'],'AllTerms':True},pg_query='anchor')
        default,params=candidate(Tables(),norm,QUERY,VECTOR)
        exact,bound=compile_request('exact-candidate',Tables(),norm,QUERY,VECTOR)
        old='ORDER BY v.vec <=> %s::public.vector FETCH FIRST'
        new='ORDER BY (v.vec <=> %s::public.vector)+0.0::double precision FETCH FIRST'
        self.assertEqual(default.count(old),1)
        self.assertEqual(exact.count(new),1)
        self.assertEqual(exact.replace(new,old),default)
        self.assertEqual(params,bound)
        for arm in ('ann-40','ann-100','default-candidate'):
            self.assertEqual(compile_request(arm,Tables(),norm,QUERY,VECTOR),(default,params))
        self.assertEqual(compile_request('reference',Tables(),norm,QUERY,VECTOR),reference(Tables(),norm,QUERY,VECTOR))

    def test_deadline_includes_loading_and_is_not_reset_per_stage(self):
        now=[100.0];budget=WholeBudget(now[0],clock=lambda:now[0])
        deadline=budget.deadline
        now[0]+=200;budget.stage('loaded-inputs')
        now[0]+=75;budget.stage('fixture-stage')
        self.assertEqual(budget.deadline,deadline)
        now[0]+=5
        with self.assertRaises(DeadlineExceeded):budget.stage('correctness-stage')
        self.assertEqual(budget.deadline,deadline)
        now[0]=399
        with self.assertRaises(DeadlineExceeded):budget.admit(reserve=0)
        self.assertEqual(budget.count,0)

    def test_actual_executor_retains_rows_and_failures_and_budget(self):
        now=[0.0];budget=WholeBudget(0,clock=lambda:now[0],ceiling=2)
        class DB:
            def execute(self,sql,params,prepare=False):
                if params:raise RuntimeError('test failure')
                class Cursor:
                    def fetchall(self):return [dict(id='synthetic',version=1,score=1.0)]
                return Cursor()
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            op=Operations(Path(tmp)/'ledger.jsonl',budget)
            rows,ms=op.execute(DB(),'SELECT 1',phase='warmup',label=0)
            budget.stage('next-stage')
            with self.assertRaises(RuntimeError):op.execute(DB(),'SELECT 1',[1],phase='timing',label=1)
            with self.assertRaises(DeadlineExceeded):op.execute(DB(),'SELECT 1',phase='timing',label=2)
            op.close();records=[json.loads(x) for x in (Path(tmp)/'ledger.jsonl').read_text().splitlines()]
            finished=[r for r in records if r['event']=='finished']
            self.assertEqual(finished[0]['result_rows'],rows)
            self.assertTrue(finished[0]['success']);self.assertFalse(finished[1]['success'])
            self.assertIsNone(finished[1]['sql_ms']);self.assertEqual(budget.count,2)
            self.assertEqual([r['ordinal'] for r in finished],[1,2])
            with self.assertRaises(FileExistsError):Operations(Path(tmp)/'ledger.jsonl',budget)

if __name__=='__main__':unittest.main()

class WholeDriverBoundary(unittest.TestCase):
    def test_actual_driver_refuses_sql_after_input_loading_consumes_deadline(self):
        from unittest.mock import patch
        import ann_shape_exact_study as study
        now=[0.0];budget=WholeBudget(0,clock=lambda:now[0])
        def slow_loader():
            now[0]=290.0
            return None
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            path=Path(tmp)
            with patch.object(study.psycopg,'connect',side_effect=AssertionError('network forbidden')):
                self.assertEqual(study.execute(budget,path,loader=slow_loader),1)
            result=json.loads((path/'result.json').read_text())
            self.assertEqual(result['error_type'],'DeadlineExceeded')
            self.assertEqual(result['sql_started'],0)
            self.assertEqual(result['deadline'],300)
