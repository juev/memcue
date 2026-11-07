"""Synthetic behavioral and privacy tests; no private inputs or model calls."""
import json
import sqlite3
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
import os

from matched_query import latest_sql, compile_query, choose_cases, safe_read, nearest, public_summary


class MatchedQueryTests(unittest.TestCase):
    def test_latest_before_status_expiry_corrections_and_selective_filters(self):
        db = sqlite3.connect(':memory:')
        db.execute('CREATE TABLE facts(id,version,status,expires,scope,kind)')
        rows = [('retired',1,'active','','global','rule'),('retired',2,'retired','','global','rule'),
                ('expired',1,'active','','global','rule'),('expired',2,'active','2020','global','rule'),
                ('corrected',1,'active','','global','rule'),('corrected',2,'active','','repo:test','note'),
                ('tie-a',1,'active','','global','rule'),('tie-b',1,'active','','global','rule')]
        db.executemany('INSERT INTO facts VALUES(?,?,?,?,?,?)',rows)
        for extra in ['', " AND f.scope='global' AND f.kind='rule'", " AND f.scope='repo:test' AND f.kind='note'"]:
            base="SELECT f.id,f.version FROM facts f WHERE f.version=(SELECT max(version) FROM facts z WHERE z.id=f.id) AND f.status='active' AND (f.expires='' OR f.expires>='2026')"+extra+' ORDER BY f.id'
            candidate=latest_sql('facts')+"SELECT f.id,f.version FROM facts f JOIN latest l ON (l.id,l.version)=(f.id,f.version) WHERE f.status='active' AND (f.expires='' OR f.expires>='2026')"+extra+' ORDER BY f.id'
            self.assertEqual(db.execute(base).fetchall(),db.execute(candidate).fetchall())
        self.assertEqual(db.execute(candidate).fetchall(),[('corrected',2)])
        db.execute("INSERT INTO facts VALUES('corrected',3,'retired','','repo:test','note')")
        self.assertEqual(db.execute(candidate).fetchall(),[])

    def test_query_values_bound_and_reference_guard_retained(self):
        class Tables:
            def table(self,s): return '"research"."p2_test_'+s+'"'
        norm={'filter':{'Today':'2026-11-06','Kinds':['rule'],'AllTerms':True},'pg_query':'synthetic:*'}
        case={'Host':'synthetic-host','Project':['synthetic-project']}
        for channel in ['lexical','vector']:
            a,pa=compile_query(Tables(),norm,case,channel,'reference',[1.0]+[0.0]*383)
            b,pb=compile_query(Tables(),norm,case,channel,'latest-cte',[1.0]+[0.0]*383)
            self.assertIn('SELECT max(version)',a)
            self.assertIn('AS MATERIALIZED',b)
            self.assertNotIn('SELECT max(version)',b)
            self.assertEqual(pa,pb)
            self.assertNotIn('synthetic:*',a)
            self.assertIn('f.created::timestamptz DESC',b)
            self.assertIn('x.lexemes @@',b)

    def test_deterministic_stratified_selection_and_nearest_rank(self):
        cases=[{'id':str(i),'language':lang} for i,lang in enumerate(['latin']*5+['cyrillic']*5+['other'])]
        counts={c['id']: i%3 for i,c in enumerate(cases)}
        self.assertEqual(choose_cases(cases,counts),choose_cases(list(reversed(cases)),counts))
        self.assertEqual(len(choose_cases(cases,counts)),8)
        self.assertEqual({c['language'] for c in choose_cases(cases,counts)},{'latin','cyrillic','other'})
        self.assertEqual(nearest([1,2,3],.95),3)

    def test_allowlist_rejects_traversal_symlinks_hardlinks_and_secret_names(self):
        with TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            root=Path(tmp);(root/'cache.json').write_text('{}')
            self.assertEqual(safe_read(root,'cache.json',{'cache.json'}),b'{}')
            for name in ['../cache.json','credentials.json','.env','loffe-research.pgpass']:
                with self.assertRaises(ValueError):safe_read(root,name,{'cache.json'})
            (root/'alias.json').symlink_to(root/'cache.json')
            with self.assertRaises(ValueError):safe_read(root,'alias.json',{'alias.json'})
            os.link(root/'cache.json',root/'linked.json')
            with self.assertRaises(ValueError):safe_read(root,'linked.json',{'linked.json'})

    def test_public_output_excludes_private_results_and_input_labels(self):
        rows=[dict(scale='current',variant='reference',channel='vector',concurrency=1,sql_ms=2,
                   hits=[{'id':'PRIVATE-FACT','content':'PRIVATE-TEXT'}],case='PRIVATE-QUERY')]
        out=json.dumps(public_summary(rows))
        for secret in ['PRIVATE','hits','case','content']:self.assertNotIn(secret,out)
        self.assertEqual(public_summary(rows)[0]['samples'],1)

if __name__=='__main__':unittest.main()
