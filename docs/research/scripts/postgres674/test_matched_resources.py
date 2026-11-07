"""Resource guard regression: unrelated historical scale tables are excluded."""
import unittest
import os
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch
import matched_study
from matched_study import size_bytes


class ResourceTests(unittest.TestCase):
    def test_five_relation_budget_does_not_include_old_denormalized_scale(self):
        class D:
            prefix='p2_test_'
            def sizes(self,db):
                return [dict(relname='p2_test_'+s,relkind='r',total_bytes=n)
                        for s,n in [('facts',10),('fact_index',2),('fact_fts',3),('fact_vectors',4),('stamps',1),('scale',99999)]]+[
                            dict(relname='p2_test_facts_pkey',relkind='i',total_bytes=100)]
        self.assertEqual(size_bytes(None,D()),20)

    def test_existing_declared_or_executed_packet_is_never_modified(self):
        with TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            root=Path(tmp)
            (root/'sentinel').write_text('preserved')
            fake=(None,None,None,[{'id':'synthetic','language':'latin'}],{'synthetic':{'filter':{}}},{})
            with patch.object(matched_study,'OUT',root), patch.object(matched_study,'FAILURE_OWNED',False), patch.object(matched_study,'inputs',return_value=fake), patch('sys.argv',['matched_study.py','declare']):
                self.assertEqual(matched_study.main(),1)
            self.assertEqual(sorted(p.name for p in root.iterdir()),['sentinel'])
            for name in ['private-declaration.json','declaration.json','run-start.json']:
                (root/name).write_text('{}')
            before={p.name:p.read_bytes() for p in root.iterdir()}
            with patch.object(matched_study,'OUT',root), patch.object(matched_study,'FAILURE_OWNED',False), patch('sys.argv',['matched_study.py','run']):
                self.assertEqual(matched_study.main(),1)
            self.assertEqual({p.name:p.read_bytes() for p in root.iterdir()},before)

if __name__=='__main__':unittest.main()
