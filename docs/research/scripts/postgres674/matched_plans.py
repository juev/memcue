"""Project private EXPLAIN plans to fixed categories and numeric observations."""
import json
from pathlib import Path
import sys
from matched_query import safe_read
from outputs import write_new_text


def summarize(packet):
    packet=Path(packet);result=[]
    names={'plans-current.json','plans-10000.json','plans-30000.json','plans-ann.json'}
    for name in sorted(names):
        scale=name[len('plans-'):-len('.json')]
        rows=json.loads(safe_read(packet,name,names))
        for ordinal,row in enumerate(rows):
            plan=row['plan'];stages=[]
            def walk(node,depth=0):
                relation=node.get('Relation Name','')
                category='other'
                for suffix,kind in [('fact_vectors','vector-relation'),('fact_index','derived-index-relation'),('fact_fts','lexical-relation'),('facts','history-relation')]:
                    if relation.endswith('_'+suffix):category=kind;break
                if node.get('Subplan Name','').startswith('SubPlan'):category='correlated-subplan'
                if node.get('Subplan Name')=='CTE latest':category='latest-materialization'
                if node['Node Type'] in ('Sort','Incremental Sort'):category='ranking-sort'
                if category!='other':
                    stages.append(dict(category=category,depth=depth,rows=node.get('Actual Rows',0),loops=node.get('Actual Loops',0),
                                       inclusive_total_ms_per_loop=node.get('Actual Total Time',0),estimated_rows=node.get('Plan Rows',0),
                                       estimated_total_cost=node.get('Total Cost',0),removed_filter_rows_per_loop=node.get('Rows Removed by Filter',0),
                                       removed_join_rows_per_loop=node.get('Rows Removed by Join Filter',0),
                                       shared_hit_blocks=node.get('Shared Hit Blocks',0),shared_read_blocks=node.get('Shared Read Blocks',0),
                                       temp_read_blocks=node.get('Temp Read Blocks',0),temp_written_blocks=node.get('Temp Written Blocks',0)))
                for child in node.get('Plans',[]):walk(child,depth+1)
            walk(plan['Plan'])
            result.append(dict(scale=scale,variant=row['variant'],channel=row['channel'],probe_ordinal=ordinal,
                               planning_ms=plan['Planning Time'],execution_ms=plan['Execution Time'],explain_client_ms=row['client_ms'],stages=stages))
    return dict(qualification='EXPLAIN node times are rounded inclusive per-loop observations, not additive CPU attribution. Vector-relation access is not isolated cosine computation. Two fixed contexts per exact arm/scale and per HNSW setting.',plans=result)


def main():
    result=summarize(sys.argv[1]);write_new_text(Path(sys.argv[2]),json.dumps(result,sort_keys=True,indent=2)+'\n')
    print('PLAN PROJECTION PASSED: fixed categories and numeric rows/loops/costs/filters only.')

if __name__=='__main__':main()
