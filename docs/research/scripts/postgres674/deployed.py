"""Content-only tables in an existing, operator-provisioned research schema."""
import re
import time
import psycopg
from psycopg.rows import dict_row
from psycopg import sql
from backend import COLUMNS, eligibility, tie_order
from embed import TAG, literal

SUFFIXES = ('facts', 'fact_index', 'fact_fts', 'fact_vectors', 'stamps', 'scale')


class Deployment:
    def __init__(self, endpoint, prefix):
        if set(endpoint) != {'host', 'port', 'dbname', 'user', 'sslmode'} or endpoint['sslmode'] != 'disable':
            raise ValueError('explicit private libpq endpoint required; passwords forbidden')
        if not re.fullmatch(r'p2_[a-z0-9]{1,20}_', prefix):
            raise ValueError('invalid research table prefix')
        self.endpoint, self.prefix = endpoint, prefix

    def table(self, suffix):
        if suffix not in SUFFIXES: raise ValueError('unknown table')
        return sql.Identifier('research', self.prefix + suffix).as_string()

    def connect(self):
        # libpq discovers PGPASSFILE; password bytes never enter Python or a DSN.
        db = psycopg.connect(**self.endpoint, connect_timeout=5, autocommit=True, row_factory=dict_row)
        db.execute("SET statement_timeout='15s'")
        db.execute('SET max_parallel_workers_per_gather=0')
        db.execute('SET jit=off')
        return db

    def preflight(self, db, require_absent=False):
        extension = db.execute("SELECT extversion,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE extname='vector'").fetchone()
        if not extension or extension['nspname'] != 'public': raise ValueError('preinstalled public vector required')
        existing = db.execute("SELECT c.oid,c.relname,c.relkind,pg_get_userbyid(c.relowner) AS owner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='research' ORDER BY c.relname").fetchall()
        if require_absent and any(row['relname'].startswith(self.prefix) for row in existing):
            raise FileExistsError('intended research table/index prefix already exists')
        identity = db.execute('SELECT version(),current_user,current_database(),pg_backend_pid()').fetchone()
        return dict(extension=extension, identity=identity, existing_relations=existing)

    def create(self, db):
        t = self.table
        with db.transaction():
            db.execute('SELECT pg_advisory_xact_lock(hashtextextended(%s,674))', (self.prefix,))
            before = self.preflight(db, require_absent=True)
            db.execute(f'''CREATE TABLE {t('facts')} (
              id text NOT NULL,version integer NOT NULL CHECK(version>0),content text NOT NULL,
              origin text NOT NULL,created text NOT NULL,status text NOT NULL CHECK(status IN ('active','retired')),
              author text NOT NULL,expires text NOT NULL,kind text NOT NULL,scope text NOT NULL,
              source text NOT NULL,evidence text NOT NULL,review text NOT NULL,prompt_version integer NOT NULL,
              channel text NOT NULL,PRIMARY KEY(id,version))''')
            db.execute(f'''CREATE TABLE {t('fact_index')} (rid bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
              id text NOT NULL UNIQUE,version integer NOT NULL,stems text NOT NULL,numbers integer NOT NULL DEFAULT 2,
              FOREIGN KEY(id,version) REFERENCES {t('facts')}(id,version))''')
            db.execute(f'''CREATE TABLE {t('fact_fts')} (rid bigint PRIMARY KEY REFERENCES {t('fact_index')},lexemes tsvector NOT NULL)''')
            db.execute(sql.SQL('CREATE INDEX {} ON {} USING gin(lexemes)').format(sql.Identifier(self.prefix+'lexemes'),sql.SQL(t('fact_fts'))))
            db.execute(f'''CREATE TABLE {t('fact_vectors')} (id text NOT NULL,tag text NOT NULL,
              dim integer NOT NULL CHECK(dim=384),text_hash text NOT NULL,version integer NOT NULL,
              vec public.vector(384) NOT NULL,PRIMARY KEY(id,tag),FOREIGN KEY(id,version) REFERENCES {t('facts')}(id,version))''')
            db.execute(f'CREATE TABLE {t("stamps")} (name text PRIMARY KEY,value text NOT NULL)')
        return before

    def import_history(self, db, facts, latest, stems):
        t = self.table
        with db.transaction():
            with db.cursor().copy(f'COPY {t("facts")} ({",".join(COLUMNS)}) FROM STDIN') as copy:
                for f in facts: copy.write_row([f[c] for c in COLUMNS])
            with db.cursor().copy(f'COPY {t("fact_index")} (id,version,stems) FROM STDIN') as copy:
                for f in latest.values():
                    if f['status'] == 'active': copy.write_row([f['id'],f['version'],stems[f['id']]])
            db.execute(f"INSERT INTO {t('fact_fts')} SELECT rid,to_tsvector('simple',stems) FROM {t('fact_index')}")
        for suffix in ('facts','fact_index','fact_fts'): db.execute(f'ANALYZE {t(suffix)}')

    def import_vectors(self, db, latest, vectors, hashes):
        with db.transaction():
            with db.cursor().copy(f'COPY {self.table("fact_vectors")} (id,tag,dim,text_hash,version,vec) FROM STDIN') as copy:
                for id, vec in vectors.items(): copy.write_row([id,TAG,384,hashes[id],latest[id]['version'],literal(vec)])
        db.execute(f'ANALYZE {self.table("fact_vectors")}')

    def search(self, db, norm, query, arm, vector=None):
        start = time.perf_counter()
        t = self.table
        tie = tie_order(query).replace('f.created DESC','f.created::timestamptz DESC')
        where, params = eligibility(norm['filter'])
        where = where.replace('FROM facts z', 'FROM '+t('facts')+' z')
        joins = f" FROM {t('facts')} f JOIN {t('fact_index')} i ON (i.id,i.version)=(f.id,f.version) JOIN {t('fact_fts')} x ON x.rid=i.rid "
        if norm['filter'].get('AllTerms'):
            where += " AND x.lexemes @@ to_tsquery('simple',%s)"; params.append(norm['pg_query'])
        def lexical(depth):
            if not norm['pg_query']: return []
            return db.execute("SELECT f.id,f.version,ts_rank(x.lexemes,to_tsquery('simple',%s)) score"+joins+
                              "WHERE "+where+" AND x.lexemes @@ to_tsquery('simple',%s) ORDER BY score DESC,"+tie+" LIMIT %s",
                              [norm['pg_query']]+params+[norm['pg_query'],depth]).fetchall()
        def exact(depth):
            if vector is None: raise ValueError('query vector required')
            return db.execute('SELECT f.id,f.version,1-(v.vec <=> %s::public.vector) score'+joins+
                              f" JOIN {t('fact_vectors')} v ON (v.id,v.version)=(f.id,f.version) WHERE "+where+
                              ' AND v.tag=%s ORDER BY v.vec <=> %s::public.vector,'+tie+' LIMIT %s',
                              [literal(vector)]+params+[TAG,literal(vector),depth]).fetchall()
        if arm == 'pg-lexical': hits = lexical(10)
        elif arm == 'pg-vector-exact': hits = exact(10)
        elif arm == 'pg-hybrid-rrf60':
            fused = {}
            for channel in (lexical(100),exact(100)):
                for rank, row in enumerate(channel,1):
                    record = fused.setdefault(row['id'],dict(id=row['id'],version=row['version'],score=0.0))
                    record['score'] += 1/(60+rank)
            hits = sorted(fused.values(),key=lambda row:(-row['score'],row['id']))[:10]
        else: raise ValueError('unknown arm')
        return dict(hits=hits,sql_ms=(time.perf_counter()-start)*1000)

    def backend_memory(self, db):
        try:
            return dict(status='measured', **db.execute('SELECT sum(total_bytes) total_bytes,sum(used_bytes) used_bytes FROM pg_backend_memory_contexts').fetchone())
        except psycopg.errors.InsufficientPrivilege:
            return dict(status='unavailable-app-role', total_bytes=None, used_bytes=None, sqlstate='42501')

    def sizes(self, db):
        return db.execute('''SELECT c.oid,c.relname,c.relkind,pg_get_userbyid(c.relowner) owner,
          pg_relation_size(c.oid) relation_bytes,pg_total_relation_size(c.oid) total_bytes,
          pg_indexes_size(c.oid) indexes_bytes FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
          WHERE n.nspname='research' AND starts_with(c.relname,%s) ORDER BY c.relname''',(self.prefix,)).fetchall()

    def scale_load(self, db, old, count):
        t = self.table
        if old == 0:
            db.execute(f'''CREATE TABLE {t('scale')} (n integer PRIMARY KEY,id text NOT NULL,version integer NOT NULL,
              content text NOT NULL,scope text NOT NULL,kind text NOT NULL,origin text NOT NULL,created text NOT NULL,
              expires text NOT NULL,status text NOT NULL,lexemes tsvector NOT NULL,vec public.vector(384) NOT NULL)''')
            db.execute(sql.SQL('CREATE INDEX {} ON {} USING gin(lexemes)').format(sql.Identifier(self.prefix+'scale_lexemes'),sql.SQL(t('scale'))))
        db.execute(f'''INSERT INTO {t('scale')}
          WITH src AS (SELECT row_number() OVER(ORDER BY f.id) rn,f.*,x.lexemes,v.vec
            FROM {t('facts')} f JOIN {t('fact_index')} i ON (i.id,i.version)=(f.id,f.version)
            JOIN {t('fact_fts')} x ON x.rid=i.rid JOIN {t('fact_vectors')} v ON (v.id,v.version)=(f.id,f.version))
          SELECT n,'replica-'||n,1,content,scope,kind,origin,created,expires,status,lexemes,vec
          FROM generate_series(%s::integer,%s::integer) n JOIN src ON src.rn=1+((n-1)%%(SELECT count(*) FROM src))''',(old+1,count))
        db.execute(f'ANALYZE {t("scale")}')

    def scale_search(self, db, norm, query, arm):
        # Each performance-only identity has exactly one version.
        where, params = eligibility(norm['filter'])
        where = where.replace(' AND f.version=(SELECT max(version) FROM facts z WHERE z.id=f.id)', '')
        if arm == 'pg-lexical':
            if not norm['pg_query']: return []
            expr = "ts_rank(f.lexemes,to_tsquery('simple',%s))"; params = [norm['pg_query']]+params
            where += " AND f.lexemes @@ to_tsquery('simple',%s)"; params.append(norm['pg_query'])
            order = 'score DESC,f.n'
        elif arm == 'pg-vector-exact':
            expr = 'f.vec <=> %s::public.vector'; params = [literal(query)]+params; order = 'score,f.n'
        else: raise ValueError('unknown scale arm')
        return db.execute('SELECT f.id,'+expr+' score FROM '+self.table('scale')+' f WHERE '+where+' ORDER BY '+order+' LIMIT 10', params).fetchall()
