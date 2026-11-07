"""Public safety projection is fail closed on private field names and labels."""
import unittest
from matched_audit import validate_public, quantile
from matched_query import plan_summary

class AuditTests(unittest.TestCase):
    def test_private_fields_and_labels_are_rejected(self):
        for v in [dict(hits=[]),dict(endpoint='secret'),dict(scope='secret'),dict(language='private-project'),dict(qualification='private text')]:
            with self.assertRaises(ValueError):validate_public(v)
        self.assertTrue(validate_public(dict(status='measured',counts={'facts':33},timings=[dict(channel='vector',scale='current',p95_ms=1)])))
    def test_nearest_rank_tail(self):
        self.assertEqual(quantile(list(range(1,25)),.95),23)
        self.assertEqual(quantile(list(range(1,25)),.99),24)
    def test_plan_projection_never_copies_sql_constants_or_names(self):
        p={'Plan':{'Node Type':'Seq Scan','Filter':'private text','Relation Name':'private table','Actual Loops':2,'Actual Rows':1}}
        v=plan_summary(p)
        self.assertNotIn('private',str(v));self.assertEqual(v['nodes'][0]['Actual Loops'],2)

if __name__=='__main__':unittest.main()
