"""Cheap deterministic checks for labels, invariants and independent metrics."""
import json,runpy,unittest
from pathlib import Path
from recompute import eligible,metrics,quantile
HERE=Path(__file__).resolve().parent
class ResearchChecks(unittest.TestCase):
    def test_fixture_is_exact_generator_output(self):
        source=runpy.run_path(str(HERE/'make_fixture.py'))
        fixture=json.loads((HERE/'fixture.json').read_text())
        self.assertEqual(fixture,dict(synthetic=True,facts=source['facts'],cases=source['cases']))
        self.assertEqual(len(fixture['facts']),38)
        self.assertEqual(sum(c['split']=='held-out' for c in fixture['cases']),26)
        for c in fixture['cases']:
            self.assertFalse(set(c['targets']) & set(c['harmful']))
            self.assertTrue(set(c['targets']+c['harmful']) <= {f['id'] for f in fixture['facts']})
    def test_metrics_count_targets_and_harm_separately(self):
        cases={'a':dict(targets=['one','two'],harmful=['bad']), 'b':dict(targets=['three'],harmful=[])}
        rows=[dict(case='a',hits=[dict(id='one'),dict(id='bad')]),dict(case='b',hits=[])]
        m=metrics(rows,cases,5)
        self.assertEqual(m['target_hit'],.5)
        self.assertEqual(m['target_recall'],.25)
        self.assertEqual(m['strict_useful_precision'],.25)
        self.assertEqual(m['known_harmful_count'],1)
        self.assertEqual(m['missing_target_count'],2)
    def test_shelf_exclusion_overrides_origin_until_explicitly_named(self):
        f=dict(status='active',expires='',created='2026-11-01T00:00:00Z',kind='rule',scope='Shelf:Research',origin='demo 2026-11-01')
        fl=dict(Origins=['demo'],ExcludePrefixes=['shelf:'])
        self.assertFalse(eligible(f,fl))
        self.assertTrue(eligible(f,dict(fl,KeepScopes=['shelf:research'])))
        self.assertFalse(eligible(dict(f,status='retired'),dict(fl,KeepScopes=['shelf:research'])))
    def test_expiry_includes_last_day_and_kind_date_filter(self):
        f=dict(status='active',expires='2026-11-06',created='2026-11-01T12:00:00Z',kind='rule',scope='global',origin='')
        self.assertTrue(eligible(f,dict(Today='2026-11-06')))
        self.assertFalse(eligible(f,dict(Today='2026-11-07')))
        self.assertFalse(eligible(f,dict(Kinds=['event'])))
        self.assertFalse(eligible(f,dict(Since='2026-11-02T00:00:00')))
    def test_nearest_rank_quantiles_are_explicit_for_small_samples(self):
        self.assertEqual(quantile(list(range(1,11)),.95),10)
        self.assertEqual(quantile(list(range(1,11)),.5),5)
if __name__=='__main__': unittest.main()
