"""Matched-history query variants and bounded, content-free aggregation helpers."""
import hashlib
import json
import math
import os
from pathlib import Path
import stat

from backend import eligibility, tie_order
from embed import TAG, literal


def sha(data):
    return hashlib.sha256(data).hexdigest()


def safe_read(root, name, allowlist):
    """Only explicit regular single-link inputs; never resolve aliases or secrets."""
    p = Path(name)
    if name not in allowlist or p.is_absolute() or '..' in p.parts:
        raise ValueError('input outside explicit allowlist')
    if any(x in name.lower() for x in ('pgpass', 'credential', 'oauth', '.env')):
        raise ValueError('credential input forbidden')
    target = Path(root).absolute() / p
    for component in (target, *target.parents):
        if component.is_symlink():
            raise ValueError('symlink input forbidden')
    info = target.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_nlink != 1:
        raise ValueError('regular single-link input required')
    fd = os.open(target, os.O_RDONLY | os.O_NOFOLLOW)
    with os.fdopen(fd, 'rb') as stream:
        if os.fstat(stream.fileno()) != info:
            raise ValueError('input changed during open')
        return stream.read()


def latest_sql(table):
    return f'WITH latest AS MATERIALIZED (SELECT id,max(version) version FROM {table} GROUP BY id) '


def eligible_query(dep, norm, variant):
    facts = dep.table('facts')
    where, params = eligibility(norm['filter'])
    correlated = 'f.version=(SELECT max(version) FROM facts z WHERE z.id=f.id)'
    if where.count(correlated) != 1:
        raise ValueError('reference eligibility changed')
    if variant == 'reference':
        return '', '', where.replace('FROM facts z', 'FROM '+facts+' z'), params
    if variant != 'latest-cte':
        raise ValueError('unknown query variant')
    return latest_sql(facts), ' JOIN latest l ON (l.id,l.version)=(f.id,f.version) ', where.replace(correlated, 'true'), params


def compile_query(dep, norm, query, channel, variant, vector, limit=10):
    """Reference text matches Deployment.search; only latest-version lookup changes."""
    cte, latest_join, where, params = eligible_query(dep, norm, variant)
    t = dep.table
    joins = f" FROM {t('facts')} f JOIN {t('fact_index')} i ON (i.id,i.version)=(f.id,f.version) JOIN {t('fact_fts')} x ON x.rid=i.rid "
    tie = tie_order(query).replace('f.created DESC', 'f.created::timestamptz DESC')
    if norm['filter'].get('AllTerms'):
        where += " AND x.lexemes @@ to_tsquery('simple',%s)"
        params.append(norm['pg_query'])
    if channel == 'lexical':
        return (cte+"SELECT f.id,f.version,ts_rank(x.lexemes,to_tsquery('simple',%s)) score"+joins+latest_join+
                "WHERE "+where+" AND x.lexemes @@ to_tsquery('simple',%s) ORDER BY score DESC,"+tie+" LIMIT %s",
                [norm['pg_query']]+params+[norm['pg_query'],limit])
    if channel != 'vector':
        raise ValueError('unknown channel')
    return (cte+'SELECT f.id,f.version,1-(v.vec <=> %s::public.vector) score'+joins+
            f" JOIN {t('fact_vectors')} v ON (v.id,v.version)=(f.id,f.version) "+latest_join+' WHERE '+where+
            ' AND v.tag=%s ORDER BY v.vec <=> %s::public.vector,'+tie+' LIMIT %s',
            [literal(vector)]+params+[TAG,literal(vector),limit])


def choose_cases(cases, filter_counts):
    buckets = {}
    for c in cases:
        key = (c['language'], bool(filter_counts[c['id']]))
        buckets.setdefault(key, []).append(c)
    for bucket in buckets.values():
        bucket.sort(key=lambda c: sha(c['id'].encode()))
    result = []
    while len(result) < min(8, len(cases)):
        for key in sorted(buckets):
            if buckets[key] and len(result) < 8:
                result.append(buckets[key].pop(0))
    return result


def nearest(values, q):
    return sorted(values)[max(0, math.ceil(len(values)*q)-1)]


def public_summary(rows):
    groups = {}
    for row in rows:
        key = tuple(row[k] for k in ('scale','variant','channel','concurrency'))
        groups.setdefault(key, []).append(row['sql_ms'])
    return [dict(zip(('scale','variant','channel','concurrency'), key), samples=len(v),
                 p50_ms=nearest(v,.5),p95_ms=nearest(v,.95),p99_ms=nearest(v,.99),max_ms=max(v))
            for key,v in sorted(groups.items(),key=lambda kv: str(kv[0]))]


def plan_summary(plan):
    """Explicit numeric/enum projection; exclude filters, aliases, names, constants."""
    nodes = []
    def walk(node, depth=0):
        keys = ('Node Type','Actual Rows','Actual Loops','Actual Total Time','Actual Startup Time',
                'Total Cost','Plan Rows','Rows Removed by Filter','Rows Removed by Join Filter',
                'Shared Hit Blocks','Shared Read Blocks','Temp Read Blocks','Temp Written Blocks')
        nodes.append(dict(depth=depth, **{k:node[k] for k in keys if k in node}))
        for child in node.get('Plans',[]):
            walk(child, depth+1)
    walk(plan['Plan'])
    return dict(planning_ms=plan.get('Planning Time'),execution_ms=plan.get('Execution Time'),nodes=nodes)
