"""Isolated research adapter; its only inputs are frozen synthetic fixtures."""
import hashlib,json,time
import psycopg
from psycopg.rows import dict_row
from embed import TAG,content_hash,literal
COLUMNS='id version content origin created status author expires kind scope source evidence review prompt_version channel'.split()
class ContractError(Exception):
    def __init__(self,code,status=409): self.code,self.status=code,status

def canonical(v): return json.dumps(v,sort_keys=True,separators=(',',':'),ensure_ascii=False)
def digest(v): return hashlib.sha256(canonical(v).encode()).hexdigest()
def connect(dsn): return psycopg.connect(dsn,autocommit=True,row_factory=dict_row)
def insert_fact(db,f):
    db.execute('INSERT INTO facts ('+','.join(COLUMNS)+') VALUES ('+','.join(['%s']*len(COLUMNS))+')',[f[c] for c in COLUMNS])
def reindex(db,f,stems):
    db.execute('DELETE FROM fact_index WHERE id=%s',(f['id'],))
    db.execute('DELETE FROM fact_vectors WHERE id=%s',(f['id'],))
    if f['status']=='active':
        rid=db.execute('INSERT INTO fact_index(id,version,stems) VALUES (%s,%s,%s) RETURNING rid',(f['id'],f['version'],stems)).fetchone()['rid']
        db.execute("INSERT INTO fact_fts VALUES (%s,to_tsvector('simple',%s))",(rid,stems))

def eligibility(f,alias='f'):
    clauses=[f"{alias}.status='active'", f'{alias}.version=(SELECT max(version) FROM facts z WHERE z.id={alias}.id)'];params=[]
    def add(s,*p): clauses.append(s);params.extend(p)
    if f.get('Kinds'): add(f'{alias}.kind = ANY(%s)',f['Kinds'])
    if f.get('Since'): add(f'left({alias}.created,19)>=%s',f['Since'])
    if f.get('Today'): add(f"({alias}.expires='' OR {alias}.expires>=%s)",f['Today'])
    ors=[]
    for sc in f.get('Scopes') or []: ors.append(f'lower({alias}.scope)=%s');params.append(sc.lower())
    for p in f.get('ScopePrefixes') or []: ors.append(f'left(lower({alias}.scope),%s)=%s');params.extend([len(p),p.lower()])
    for o in f.get('Origins') or []: ors.append(f'left({alias}.origin,%s)=%s');params.extend([len(o)+1,o+' '])
    if ors: clauses.append('('+' OR '.join(ors)+')')
    for p in f.get('ExcludePrefixes') or []:
        add(f'(left(lower({alias}.scope),%s)<>%s OR lower({alias}.scope)=ANY(%s))',len(p),p.lower(),[s.lower() for s in f.get('KeepScopes') or []])
    return ' AND '.join(clauses),params

def search(db,norm,query,arm,vector=None,limit=10,authorized=None):
    start=time.perf_counter();where,params=eligibility(norm['filter'])
    if authorized is not None:
        where+=' AND lower(f.scope)=ANY(%s)';params.append([x.lower() for x in authorized])
    # AllWords constrains all channels; ordinary vector-only queries permit paraphrases.
    if norm['filter'].get('AllTerms'):
        where+=" AND t.lexemes @@ to_tsquery('simple',%s)";params.append(norm['pg_query'])
    joins=' FROM facts f JOIN fact_index i ON (i.id,i.version)=(f.id,f.version) JOIN fact_fts t ON t.rid=i.rid '
    def lexical(depth):
        if not norm['pg_query']: return []
        return db.execute("SELECT f.id,f.version,ts_rank(t.lexemes,to_tsquery('simple',%s)) AS score"+joins+"WHERE "+where+" AND t.lexemes @@ to_tsquery('simple',%s) ORDER BY score DESC, "+tie_order(query)+" LIMIT %s",[norm['pg_query']]+params+[norm['pg_query'],depth]).fetchall()
    def vectors(depth):
        if vector is None: raise ContractError('embedding_unavailable',503)
        return db.execute('SELECT f.id,f.version,1-(v.vec <=> %s::vector) AS score'+joins+' JOIN fact_vectors v ON (v.id,v.version)=(f.id,f.version) WHERE '+where+' AND v.tag=%s ORDER BY v.vec <=> %s::vector, '+tie_order(query)+' LIMIT %s',[literal(vector)]+params+[TAG,literal(vector),depth]).fetchall()
    if arm=='pg-lexical': hits=lexical(limit)
    elif arm=='pg-vector-exact': hits=vectors(limit)
    elif arm=='pg-hybrid-rrf60':
        fused={}
        for channel in [lexical(100),vectors(100)]:
            for rank,row in enumerate(channel,1):
                v=fused.setdefault(row['id'],dict(id=row['id'],version=row['version'],score=0.0));v['score']+=1/(60+rank)
        hits=sorted(fused.values(),key=lambda r:(-r['score'],r['id']))[:limit]
    else: raise ContractError('unknown_arm',400)
    return dict(hits=hits,sql_ms=(time.perf_counter()-start)*1000,embed_ms=0.0)

def tie_order(query):
    # Values are bound as SQL literals; never interpolate caller strings unescaped.
    host=psycopg.sql.Literal('host:'+query['Host']).as_string()
    projects=[psycopg.sql.Literal(x+' ').as_string() for x in query['Project']]
    project=" OR ".join('left(f.origin,'+str(len(x)+1)+')='+p for x,p in zip(query['Project'],projects)) or 'false'
    scopes=','.join(psycopg.sql.Literal('repo:'+x).as_string() for x in query['Project']) or "''"
    return f"CASE WHEN f.scope={host} THEN 0 WHEN f.scope IN ({scopes}) OR {project} THEN 1 WHEN f.scope='global' THEN 2 ELSE 3 END, f.created DESC,f.id"

def mutate(db,principal,key,request,normalize,authorized):
    if not key or len(key)>128: raise ContractError('idempotency_key_required',400)
    h=digest(request)
    with db.transaction():
        db.execute("SET LOCAL statement_timeout='3000ms'")
        # Lock keys across simultaneous identical requests; collisions only serialize unrelated keys.
        db.execute('SELECT pg_advisory_xact_lock(hashtextextended(%s,0))',(canonical([principal,key]),))
        prior=db.execute('SELECT * FROM operations WHERE principal=%s AND key=%s',(principal,key)).fetchone()
        if prior:
            if prior['request_hash']!=h: raise ContractError('idempotency_payload_mismatch')
            return prior['response']
        op=request.get('op'); f=request.get('fact'); factid=request.get('id',f.get('id') if f else None)
        if not isinstance(factid,str) or not factid: raise ContractError('invalid_id',400)
        db.execute('SELECT pg_advisory_xact_lock(hashtextextended(%s,1))',(factid,))
        cur=db.execute('SELECT * FROM facts WHERE id=%s ORDER BY version DESC LIMIT 1',(factid,)).fetchone()
        if op=='add':
            if cur: raise ContractError('already_exists')
            f=dict(f)
            if set(f)!=set(COLUMNS): raise ContractError('invalid_fields',400)
            if f['version']!=1 or f['status']!='active': raise ContractError('invalid_add',400)
        else:
            if not cur: raise ContractError('not_found',404)
            if cur['scope'].lower() not in authorized: raise ContractError('forbidden',403)
            if request.get('expected_version')!=cur['version']: raise ContractError('version_conflict')
            f=dict(cur);f['version']+=1
            if op=='correct':
                if cur['status']=='retired': raise ContractError('retired')
                f['content']=request['content']
                if f['content']!=cur['content']: f['channel']=''
            elif op=='retire': f['status']='retired'
            elif op=='restore':
                if cur['status']!='retired': raise ContractError('not_retired')
                f['status']='active'
            else: raise ContractError('unsupported_operation',400)
        if f['scope'].lower() not in authorized: raise ContractError('forbidden',403)
        if not f['content'].strip() or len(f['content'])>16000: raise ContractError('invalid_content',400)
        # Research uses caller-supplied deterministic timestamp/host, validated against principal by service.
        f['author']=request['caller']['host'];f['created']=request['now']
        insert_fact(db,f);reindex(db,f,normalize(f['content']))
        reply=dict(id=f['id'],version=f['version'],status=f['status'])
        db.execute('INSERT INTO operations VALUES (%s,%s,%s,%s::jsonb)',(principal,key,h,canonical(reply)))
        return reply

def publish_vector(db,id,version,text_hash,vec,tag=TAG):
    with db.transaction():
        db.execute('SELECT pg_advisory_xact_lock(hashtextextended(%s,1))',(id,))
        row=db.execute('SELECT * FROM facts WHERE id=%s ORDER BY version DESC LIMIT 1',(id,)).fetchone()
        if not row or row['version']!=version or row['status']!='active' or content_hash(row['content'])!=text_hash: return False
        db.execute('INSERT INTO fact_vectors VALUES (%s,%s,384,%s,%s,%s::vector) ON CONFLICT (id,tag) DO UPDATE SET version=excluded.version,text_hash=excluded.text_hash,vec=excluded.vec',(id,tag,text_hash,version,literal(vec)))
        return True
