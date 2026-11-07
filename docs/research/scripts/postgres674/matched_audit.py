"""Offline saved-record audit; never connects to PostgreSQL or an embedding model."""
import collections
import json
import math
from pathlib import Path
import sys

from matched_query import safe_read, sha
from outputs import write_new_text


def quantile(values,q):
    values=sorted(values)
    return values[math.ceil(q*len(values))-1]


def validate_public(value):
    """Reject text-bearing fields and non-enumerated string values recursively."""
    forbidden={'id','case','hits','query','params','sql','content','origin','scope','endpoint','host','user','dbname','prefix','source_packet'}
    allowed_strings={'measured','current','10000','30000','reference','latest-cte','ann-40','ann-100','lexical','vector',
                     'latin','cyrillic','other','unrestricted','scope-filtered','NOT_NEEDED_FOR_THIS_DECLARATION','ANN_MEASURED',
                     'INDEX_NOT_USED_FOR_ALL_PROBED_PLANS','unmeasured-exact-container-not-discovered','measured-samples',
                     'remaining-matched-study'}
    # Explanatory strings and source hashes are checked at their fixed field only.
    explanations={
        'Linux cumulative process high-water RSS, includes Python cached normalization/JSON and libpq, excludes PG server',
        'Connection establishment and session initialization measured separately; no Python process startup in timed searches.',
        'container sampling maxima, not continuous peaks or backend RSS; container includes other activity',
        'Latency under HNSW settings only counts as ANN where actual plan uses HNSW. Tied replicas make ID recall and kth-boundary validity different.',
        'natural relevance/agent outcomes','remote-client network bytes/latency','monthly cost','production protocol/writes/recovery','independent acceptance and CI',
        'Nearest rank. 24 dependent samples per group from eight contexts; p95 rank23, p99/max rank24. No stable tail estimate or new semantic diversity.'
    }
    def walk(v,path=()):
        if isinstance(v,dict):
            if forbidden & set(v):raise ValueError('private field in public output')
            for key,item in v.items():walk(item,path+(key,))
        elif isinstance(v,list):
            for item in v:walk(item,path)
        elif isinstance(v,str):
            if v in explanations:return
            if v not in allowed_strings and not (len(v)==64 and all(c in '0123456789abcdef' for c in v)):
                raise ValueError('non-enumerated string in aggregate')
        elif v is not None and not isinstance(v,(bool,int,float)):
            raise ValueError('unexpected aggregate type')
    walk(value)
    return True


def audit(packet):
    packet=Path(packet)
    names={'aggregates.json','declaration.json','private-declaration.json','timings.jsonl','complete.json',
           'final-readback.json','source-schema.json','schema-10000.json','schema-30000.json',
           'load-10000.json','load-30000.json','equality-current.jsonl','equality-10000.jsonl','equality-30000.jsonl',
           'connections.jsonl','workloads.jsonl','resources.jsonl','synthetic-db.json'}
    read=lambda n:json.loads(safe_read(packet,n,names))
    result=read('aggregates.json');decl=read('declaration.json');cfg=read('private-declaration.json')
    assert read('complete.json')['exit']==0
    records=[json.loads(line) for line in safe_read(packet,'timings.jsonl',names).decode().splitlines()]
    groups=collections.defaultdict(list)
    ranks={};rank_checks=0
    for row in records:
        group=tuple(row[k] for k in ('scale','variant','channel','concurrency'))
        groups[group].append(row['sql_ms'])
        if row['variant'] in ('reference','latest-cte'):
            key=(row['scale'],row['channel'],row['case'])
            if key in ranks:assert row['hits']==ranks[key];rank_checks+=1
            else:ranks[key]=row['hits']
    for saved in result['timings']:
        group=tuple(saved[k] for k in ('scale','variant','channel','concurrency'))
        values=groups.pop(group)
        assert len(values)==saved['samples']==24
        for key,q in [('p50_ms',.5),('p95_ms',.95),('p99_ms',.99)]:assert saved[key]==quantile(values,q)
        assert saved['max_ms']==max(values)
    assert not groups
    equality_checks=0
    for scale in ('current','10000','30000'):
        rows=[json.loads(line) for line in safe_read(packet,'equality-'+scale+'.jsonl',names).decode().splitlines()]
        assert len(rows)==3*(128 if scale=='current' else 8)
        eligible={row['case']:{(r['id'],r['version']) for r in row['rows']} for row in rows if row['type']=='eligible'}
        for row in rows:
            assert row['equal']
            if row['type']!='eligible':
                assert all((h['id'],h['version']) in eligible[row['case']] for h in row['hits'])
                assert row['unchanged_reference']
                equality_checks+=1
    shape=read('source-schema.json')
    for scale in ('10000','30000'):
        assert read('schema-'+scale+'.json')==shape
        load=read('load-'+scale+'.json')
        assert all(v==0 for v in load['exact_sql_readback_differences'].values())
        assert load['counts']['primary_ids']==int(scale)
    final=read('final-readback.json')
    assert final['source_history_equal'] and final['preexisting_relation_identity_equal']
    assert final['total_added_bytes']==result['added_relation_bytes']<=decl['budgets']['total_added_relation_bytes']
    assert result['added_history_rows']<=decl['budgets']['added_history_rows']
    assert result['provider_calls']==result['new_embeddings']==0
    assert result['ann']['triggered']==any(r['scale']=='30000' and r['channel']=='vector' and r['p95_ms']>100 for r in result['timings'] if r['variant'] in ('reference','latest-cte'))
    connection_rows=[json.loads(line) for line in safe_read(packet,'connections.jsonl',names).decode().splitlines()]
    cm=[r['connection_ms'] for r in connection_rows]
    output=dict(status='measured',declaration_sha256=result['declaration_sha256'],counts=result['counts'],equality=result['equality'],
                selected_strata=result['selected_strata'],timings=result['timings'],ann=result['ann'],
                added_history_rows=result['added_history_rows'],added_relation_bytes=result['added_relation_bytes'],driver=result['driver'],
                connections=dict(samples=len(cm),p50_ms=quantile(cm,.5),p95_ms=quantile(cm,.95),max_ms=max(cm),
                                 qualification='Connection establishment and session initialization measured separately; no Python process startup in timed searches.'),
                server=result['server'],source_preserved=result['source_preserved'],provider_calls=0,new_embeddings=0,unknowns=result['unknowns'],
                audit=dict(timed_rows=len(records),repeated_rank_comparisons=rank_checks,equality_rank_pairs=equality_checks,
                           matched_schema_indexes=True,copy_values_sql_readback_equal=True,public_safety=True),
                quantiles='Nearest rank. 24 dependent samples per group from eight contexts; p95 rank23, p99/max rank24. No stable tail estimate or new semantic diversity.')
    validate_public(output)
    return output


def main():
    packet=Path(sys.argv[1]);out=Path(sys.argv[2])
    result=audit(packet)
    write_new_text(out,json.dumps(result,sort_keys=True,indent=2)+'\n')
    print('AUDIT PASSED: saved quantiles, repetitions, eligibility, schemas, copy readback and public projection.')

if __name__=='__main__':main()
