"""Genuine synthetic-only SQLite-content / PG-hybrid adapter for two native episodes.

No credentials are opened by Python. libpq consumes the existing private passfile.
The setup requires an absent owned prefix and retains all created relations.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import sys
import time
from backend import COLUMNS
from deployed import Deployment
from embed import Embedder, content_hash, TAG
from normalize import Normalizer
from outputs import create_output_directory, write_new_text


def load(path): return json.loads(Path(path).read_text())
def save(path, value): write_new_text(path, json.dumps(value, indent=2, ensure_ascii=False)+'\n')
def digest(value): return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'), ensure_ascii=False).encode()).hexdigest()


def deployment(config):
    os.environ['PGPASSFILE'] = config['passfile']
    return Deployment(load(config['endpoint']), config['prefix'])


def prepare(config):
    out = create_output_directory(config['data'])
    declaration = load(config['declaration']); facts = declaration['facts']
    assert len(facts) == 3 and all(f['version'] == 1 and f['status'] == 'active' and f['scope'] == 'global' for f in facts)
    normalizer = Normalizer(config['bridge']); stems = {f['id']: normalizer.stems(f['content']) for f in facts}
    fixture = {'facts': [{**f, 'keywords': '', 'queries': ''} for f in facts], 'cases': []}
    save(out/'sqlite-fixture.json', fixture)
    argv = [config['bridge'], str(out/'sqlite-fixture.json'), str(out/'sqlite')]
    built = subprocess.run(argv, capture_output=True, text=True, timeout=15)
    save(out/'sqlite-build.json', {'argv': argv, 'exit': built.returncode, 'stdout': built.stdout, 'stderr': built.stderr})
    if built.returncode: raise RuntimeError('native SQLite fixture construction failed')
    # The existing bridge also builds an unused full-index cache; zero cases and
    # zero full-index searches are executed. Only sqlite-content enters an episode.
    with sqlite3.connect('file:'+str(out/'sqlite/sqlite-content.db')+'?mode=ro', uri=True) as db:
        rows = db.execute('SELECT '+','.join(COLUMNS)+' FROM facts ORDER BY id,version').fetchall()
    expected = sorted(facts, key=lambda f: (f['id'], f['version']))
    assert rows == [tuple(f[c] for c in COLUMNS) for f in expected]
    embedder = Embedder(config['model_cache']); vectors, milliseconds = embedder.encode([f['content'] for f in facts], 'passage: ')
    vectors = {f['id']: v for f, v in zip(facts, vectors)}
    hashes = {f['id']: content_hash(f['content']) for f in facts}; latest = {f['id']: f for f in facts}
    deploy = deployment(config)
    with deploy.connect() as db:
        # Catalog metadata only; no existing user relation is read or modified.
        absent = db.execute("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='research' AND starts_with(c.relname,%s)", (config['prefix'],)).fetchone()['n'] == 0
        assert absent, 'owned prefix must be absent'
        save(out/'namespace-before.json', {'prefix': config['prefix'], 'absent': absent})
        before = deploy.create(db)
        deploy.import_history(db, facts, latest, stems); deploy.import_vectors(db, latest, vectors, hashes)
        readback = db.execute('SELECT '+','.join(COLUMNS)+' FROM '+deploy.table('facts')+' ORDER BY id,version').fetchall()
        assert readback == expected, 'new remote fact read-back mismatch'
        vector_rows = db.execute('SELECT id,tag,dim,text_hash,version,vec::text AS vector FROM '+deploy.table('fact_vectors')+' ORDER BY id').fetchall()
        assert len(vector_rows) == 3 and all(r['tag'] == TAG and r['version'] == 1 and r['dim'] == 384 and r['text_hash'] == hashes[r['id']] for r in vector_rows)
        save(out/'owned-readback.json', {'facts': readback, 'facts_sha256': digest(readback),
            'vector_rows': vector_rows, 'owned_relations': deploy.sizes(db), 'identity': before['identity'],
            'prefix_initially_absent': True, 'embedding_ms': milliseconds, 'embedding_tag': TAG,
            'sqlite_history_exact': True, 'remote_history_exact': True})
    save(out/'ready.json', {'ready': True, 'facts': 3, 'vectors': 3, 'prefix': config['prefix'],
        'source_facts_sha256': digest(expected), 'no_preexisting_relation_data_access': True,
        'auxiliary_fields_empty_sqlite_and_absent_pg': True, 'ANN': False})
    print('synthetic setup ready; three facts/vectors; remote read-back exact')


def search(config, arm, arguments):
    assert arm in ('sqlite-content', 'pg-hybrid-rrf60')
    assert isinstance(arguments, dict) and set(arguments) == {'query'} and isinstance(arguments['query'], str)
    assert arguments['query'].strip() and len(arguments['query']) <= 4096
    out = Path(config['data']); assert load(out/'ready.json')['ready']
    query = {'Text': arguments['query'], 'N': 10, 'MinScore': 0, 'Project': [],
             'Host': 'synthetic-client', 'In': ['global'], 'Today': '2026-11-07', 'AllWords': False}
    normalizer = Normalizer(config['bridge']); normalized = normalizer.query(query)
    started = time.monotonic(); embed_ms = None
    if arm == 'sqlite-content':
        argv = [config['bridge'], 'search-db', str(out/'sqlite/sqlite-content.db')]
        result = subprocess.run(argv, input=json.dumps(query), capture_output=True, text=True, timeout=5)
        if result.returncode: raise RuntimeError('native SQLite search failed')
        raw = json.loads(result.stdout); hits = [{'id': f['ID'], 'version': f['Version'], 'content': f['Content'],
            'status': f['Status'], 'scope': f['Scope'], 'expires': f['Expires']} for f in (raw.get('hits') or [])]
    else:
        embedder = Embedder(config['model_cache']); vector, embed_ms = embedder.encode([arguments['query']], 'query: ')
        deploy = deployment(config)
        with deploy.connect() as db:
            db.execute("SET statement_timeout='5000ms'")
            raw = deploy.search(db, normalized, query, arm, vector[0])
            hits = []
            for h in raw['hits']:
                f = db.execute('SELECT id,version,content,status,scope,expires FROM '+deploy.table('facts')+' WHERE id=%s AND version=%s', (h['id'], h['version'])).fetchone()
                if not f: raise RuntimeError('returned identity has no owned fact')
                hits.append(dict(f))
    declared = {f['id']: f for f in load(config['declaration'])['facts']}
    for f in hits:
        expected = declared[f['id']]
        assert f['version'] == expected['version'] and f['status'] == 'active' and f['expires'] == '' and f['scope'] == 'global'
        assert f['content'] == expected['content']
    return {'hits': hits, 'search_executed': True, 'arm': arm,
            'elapsed_seconds': time.monotonic()-started, 'embedding_ms': embed_ms,
            'fact_version_eligibility_verified': True}


def main():
    parser = argparse.ArgumentParser(description=__doc__); parser.add_argument('operation', choices=['prepare', 'search'])
    parser.add_argument('config'); parser.add_argument('arm', nargs='?'); args = parser.parse_args()
    config = load(args.config)
    if args.operation == 'prepare': prepare(config)
    else: print(json.dumps(search(config, args.arm, json.load(sys.stdin)), ensure_ascii=False))


if __name__ == '__main__': main()
