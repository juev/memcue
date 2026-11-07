"""Read-only vector shape, accounting, and content-free projections for #674."""
import hashlib
import json
import math
import os
from pathlib import Path
import threading
import time

from backend import tie_order
from embed import TAG, literal
from matched_query import compile_query, eligible_query, safe_read, sha
from outputs import create_output_directory, write_new_text

SPACE = 'cosine-e5-384'


def validate_vector(vector, space=SPACE):
    if space != SPACE or len(vector) != 384:
        raise ValueError('invalid vector space or dimensions')
    if not all(isinstance(x, (float, int)) and not isinstance(x, bool) and math.isfinite(x) for x in vector):
        raise ValueError('nonfinite vector')
    if abs(sum(x*x for x in vector)-1) > 1e-5:
        raise ValueError('normalized vector required')


def candidate(dep, norm, query, vector, limit=10, space=SPACE):
    validate_vector(vector, space)
    if not isinstance(limit, int) or isinstance(limit, bool) or limit < 1 or limit > 10:
        raise ValueError('invalid limit')
    _, _, where, params = eligible_query(dep, norm, 'reference')
    if norm['filter'].get('AllTerms'):
        where += " AND x.lexemes @@ to_tsquery('simple',%s)"
        params.append(norm['pg_query'])
    t = dep.table
    tie = tie_order(query).replace('f.created DESC', 'f.created::timestamptz DESC')
    # OFFSET 0 keeps the eligibility subquery correlated. No materialized shortlist
    # is filtered after top-k; version/status/expiry and all filter clauses are here.
    sql = ('SELECT f.id,f.version,1-knn.distance score FROM ('
           'SELECT v.id,v.version,v.vec <=> %s::public.vector distance FROM '+t('fact_vectors')+' v '
           'WHERE v.tag=%s AND EXISTS (SELECT 1 FROM '+t('facts')+' f '
           'JOIN '+t('fact_index')+' i ON (i.id,i.version)=(f.id,f.version) '
           'JOIN '+t('fact_fts')+' x ON x.rid=i.rid '
           'WHERE (f.id,f.version)=(v.id,v.version) AND '+where+' OFFSET 0) '
           'ORDER BY v.vec <=> %s::public.vector FETCH FIRST %s ROWS WITH TIES'
           ') knn JOIN '+t('facts')+' f ON (f.id,f.version)=(knn.id,knn.version) '
           'ORDER BY knn.distance,'+tie+' LIMIT %s')
    return sql, [literal(vector), TAG]+params+[literal(vector), limit, limit]


def reference(dep, norm, query, vector, limit=10):
    validate_vector(vector)
    return compile_query(dep, norm, query, 'vector', 'reference', vector, limit)


def save(path, value):
    write_new_text(path, json.dumps(value, sort_keys=True, ensure_ascii=False, indent=2)+'\n')


class Operations:
    """Every application SQL attempt is journaled, including failures and skips.

    Connection options configure sessions without SET/PREPARE SQL. libpq protocol
    startup/close messages are not counted as SQL statements. No implicit BEGIN:
    connections are autocommit, default_transaction_read_only=on.
    """
    def __init__(self, path, ceiling, seconds):
        self.path = Path(path)
        self.stream = self.path.open('x')
        self.path.chmod(0o600)
        self.lock = threading.Lock()
        self.count = 0
        self.ceiling = ceiling
        self.deadline = time.monotonic()+seconds

    def execute(self, db, sql, params=(), *, phase, label, prepare=False):
        with self.lock:
            if time.monotonic() >= self.deadline or self.count >= self.ceiling:
                raise TimeoutError('prospective SQL or wall budget exhausted')
            self.count += 1
            ordinal = self.count
        if not (sql.startswith('SELECT ') or sql.startswith('WITH ') or sql.startswith('EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT ')):
            raise ValueError('read-only query category required')
        row = dict(ordinal=ordinal, phase=phase, label=label, executed_sql=True,
                   sql=sql, params=params, prepare=prepare, sql_ms=None, success=False)
        start = time.monotonic()
        try:
            values = db.execute(sql, params, prepare=prepare).fetchall()
            row.update(success=True, sql_ms=(time.monotonic()-start)*1000, rows=len(values))
            if phase in ('timing', 'warmup'):
                row['result_rows'] = values
            return values, row['sql_ms']
        except BaseException as error:
            row.update(error_type=type(error).__name__, sqlstate=getattr(error, 'sqlstate', None),
                       attempted_wall_ms=(time.monotonic()-start)*1000)
            raise
        finally:
            with self.lock:
                self.stream.write(json.dumps(row, ensure_ascii=False)+'\n')
                self.stream.flush()

    def skip(self, phase, label):
        with self.lock:
            self.stream.write(json.dumps(dict(phase=phase, label=label, executed_sql=False,
                                             sql_ms=None, success=None))+'\n')
            self.stream.flush()

    def close(self):
        self.stream.close()


def agreement(hits, truth, eligible, tolerance=3e-6):
    keys = lambda rows: [(r['id'], r['version']) for r in rows]
    expected = keys(truth)
    actual = keys(hits)
    k = len(expected)
    boundary = 1-truth[-1]['score'] if truth else None
    return dict(exact_equal=actual == expected and all(abs(a['score']-b['score']) <= tolerance for a,b in zip(hits,truth)),
                strict_id_version_recall_at10=len(set(actual)&set(expected))/k if k else None,
                eligible=all(x in eligible for x in actual),
                underfill=max(0,k-len(hits)), returned=len(hits), exact_count=k,
                boundary_valid_fraction=(sum(1-h['score'] <= boundary+tolerance for h in hits)/len(hits)
                                         if hits and boundary is not None else None))


def plan_projection(plan, index_name):
    nodes = []
    def walk(node):
        nodes.append(dict(node_type=node['Node Type'], hnsw=node.get('Index Name') == index_name,
                          rows=node.get('Actual Rows'), loops=node.get('Actual Loops'),
                          removed_filter=node.get('Rows Removed by Filter',0),
                          inclusive_ms=node.get('Actual Total Time'),
                          sort_keys=len(node.get('Sort Key',[])), ordered=bool(node.get('Order By'))))
        for child in node.get('Plans',[]): walk(child)
    walk(plan['Plan'])
    return dict(hnsw_used=any(n['hnsw'] for n in nodes), planning_ms=plan.get('Planning Time'),
                execution_ms=plan.get('Execution Time'), nodes=nodes)
