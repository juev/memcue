"""Correctness-only distance ordering and a single whole-invocation SQL budget."""
import json
import threading
import time

from ann_shape import candidate, reference
from outputs import write_new_text


class DeadlineExceeded(TimeoutError):
    pass


class WholeBudget:
    def __init__(self, started, seconds=300, ceiling=565, clock=time.monotonic):
        self.started=started;self.deadline=started+seconds;self.ceiling=ceiling
        self.clock=clock;self.count=0;self.lock=threading.Lock()

    def remaining(self):
        return self.deadline-self.clock()

    def stage(self, name, reserve=15, minimum=5.5):
        # A stage never assigns a new deadline. Loading/admission share this clock.
        if self.remaining() < reserve+minimum:
            raise DeadlineExceeded('whole invocation deadline admission refused')

    def admit(self, reserve=15):
        with self.lock:
            self.stage('sql',reserve=reserve)
            if self.count >= self.ceiling:raise DeadlineExceeded('SQL ceiling exhausted')
            self.count+=1
            return self.count


def exact_candidate(dep,norm,query,vector,limit=10):
    sql,params=candidate(dep,norm,query,vector,limit)
    before='ORDER BY v.vec <=> %s::public.vector FETCH FIRST'
    after='ORDER BY (v.vec <=> %s::public.vector)+0.0::double precision FETCH FIRST'
    if sql.count(before)!=1:raise ValueError('frozen candidate order changed')
    return sql.replace(before,after),params


def compile_request(arm,dep,norm,query,vector,limit=10):
    if arm=='exact-candidate':return exact_candidate(dep,norm,query,vector,limit)
    if arm=='reference':return reference(dep,norm,query,vector,limit)
    if arm in ('ann-40','ann-100','default-candidate'):return candidate(dep,norm,query,vector,limit)
    raise ValueError('unknown request arm')


class Journal:
    def __init__(self,path):
        # Reuse the exclusive, symlink-refusing real output helper.
        write_new_text(path,'')
        self.stream=path.open('a');self.lock=threading.Lock()
    def write(self,value):
        with self.lock:
            self.stream.write(json.dumps(value,ensure_ascii=False)+'\n');self.stream.flush()
    def close(self):self.stream.close()


class Operations:
    def __init__(self,path,budget):
        self.budget=budget;self.journal=Journal(path)
        self.completed=0;self.failed=0;self.lock=threading.Lock()
    def execute(self,db,sql,params=(),*,phase,label,reserve=15):
        if not sql.startswith(('SELECT ','WITH ','EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) SELECT ')):
            raise ValueError('read-only SQL category required')
        ordinal=self.budget.admit(reserve)
        self.journal.write(dict(event='started',ordinal=ordinal,phase=phase,label=label,
                                executed_sql=True,sql=sql,params=params,prepare=False))
        row=dict(event='finished',ordinal=ordinal,phase=phase,label=label,executed_sql=True,
                 success=False,sql_ms=None,result_rows=None)
        start=self.budget.clock()
        try:
            rows=db.execute(sql,params,prepare=False).fetchall()
            row.update(success=True,sql_ms=(self.budget.clock()-start)*1000,result_rows=rows)
            with self.lock:self.completed+=1
            return rows,row['sql_ms']
        except BaseException as error:
            row.update(error_type=type(error).__name__,sqlstate=getattr(error,'sqlstate',None),
                       attempted_wall_ms=(self.budget.clock()-start)*1000)
            with self.lock:self.failed+=1
            raise
        finally:self.journal.write(row)
    def close(self):self.journal.close()
