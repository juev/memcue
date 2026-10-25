"""Build the sub-base variants of #392 and score the known-item questions on each.

usage: python3 variants.py WORKDIR

WORKDIR holds facts.db (a copy of a base), memcue-main (the binary), subset.json (the 60 targets and the 400
facts of the sub-base), qgen.txt.out (the questions, question_prompt.md) and tr-*.txt.out (the translations,
translate_prompt.md). Every variant is a copy of the base cut to the sub-base, with content and queries replaced;
the search runs with its own state directory. Experiment only, not part of memcue.
"""
import json, os, shutil, sqlite3, subprocess, sys, statistics, glob
S = sys.argv[1]
BIN = S + '/memcue-main'
sub = json.load(open(S + '/subset.json'))
ids = {r['id'] for r in sub['sub']}
tr = {}
for f in sorted(glob.glob(S + '/tr-*.txt.out')):
    for line in open(f):
        line = line.strip()
        if line.startswith('{'):
            d = json.loads(line); tr[d['id']] = d
qs = {}
for line in open(S + '/qgen.txt.out'):
    line = line.strip()
    if line.startswith('{'):
        d = json.loads(line); qs[d['id']] = d
missing = ids - set(tr)
print('translations', len(tr), 'missing', len(missing), 'questions', len(qs))
lang = {r['id']: r['lang'] for r in sub['sub']}

def build(name, content_of, queries_of):
    path = f'{S}/v-{name}.db'
    shutil.copy(S + '/facts.db', path)
    db = sqlite3.connect(path)
    db.execute('CREATE TEMP TABLE keep(id TEXT PRIMARY KEY)')
    db.executemany('INSERT INTO keep VALUES (?)', [(i,) for i in ids])
    db.execute('DELETE FROM facts WHERE id NOT IN (SELECT id FROM keep)')
    for i in ids:
        v, c, q = db.execute('SELECT version, content, queries FROM facts WHERE id=? ORDER BY version DESC LIMIT 1', (i,)).fetchone()
        db.execute('UPDATE facts SET content=?, queries=? WHERE id=? AND version=?', (content_of(i, c), queries_of(i, c, q), i, v))
    db.execute('DELETE FROM fact_index'); db.execute("INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')")
    db.commit(); db.close()
    env = dict(os.environ, MEMCUE_FACTS=path, MEMCUE_STATE_DIR=f'{S}/st-{name}')
    subprocess.run([BIN, 'reindex'], env=env, check=True, capture_output=True)
    return path

other = lambda i: 'en' if lang[i] == 'ru' else 'ru'
V = {
    'A': (lambda i, c: c, lambda i, c, q: q),
    'A-noq': (lambda i, c: c, lambda i, c, q: ''),
    'B-en': (lambda i, c: tr.get(i, {}).get('en', c), lambda i, c, q: q),
    'B-en-noq': (lambda i, c: tr.get(i, {}).get('en', c), lambda i, c, q: ''),
    'C-ru': (lambda i, c: tr.get(i, {}).get('ru', c), lambda i, c, q: q),
    'C-ru-noq': (lambda i, c: tr.get(i, {}).get('ru', c), lambda i, c, q: ''),
    'E-both': (lambda i, c: c, lambda i, c, q: (q + ', ' if q else '') + tr.get(i, {}).get(other(i), '')),
    'E-both-noq': (lambda i, c: c, lambda i, c, q: tr.get(i, {}).get(other(i), '')),
}
res = {}
for name, (cf, qf) in V.items():
    path = build(name, cf, qf)
    env = dict(os.environ, MEMCUE_FACTS=path, MEMCUE_STATE_DIR=f'{S}/st-{name}')
    db = sqlite3.connect(path)
    basechars = db.execute("SELECT sum(length(content)) FROM facts f WHERE version=(SELECT max(version) FROM facts g WHERE g.id=f.id)").fetchone()[0]
    out = {}
    for t in sub['targets']:
        if t not in qs: continue
        for ql in ('ru', 'en'):
            p = subprocess.run([BIN, 'search', '-json', '-n', '5', '-cwd', os.path.expanduser('~'), '--', qs[t][ql]], env=env, capture_output=True, text=True, check=True)
            hits = json.loads(p.stdout) if p.stdout.strip() else []
            ids5 = [h['id'] for h in hits]
            rank = ids5.index(t) + 1 if t in ids5 else 0
            out[(t, ql)] = (rank, sum(len(h['content']) for h in hits))
    res[name] = (out, basechars)

def cell(out, fl, ql):
    rs = [r for (t, q), (r, _) in out.items() if q == ql and (fl is None or lang[t] == fl)]
    h1 = sum(r == 1 for r in rs); h5 = sum(r > 0 for r in rs); mrr = sum(1 / r for r in rs if r) / len(rs)
    return f'{h5}/{len(rs)} ({h1}@1, MRR {mrr:.2f})'
print('| variant | base chars | ru fact, ru q | ru fact, en q | en fact, ru q | en fact, en q | all, hit@5 | top-5 chars |')
print('|---|---|---|---|---|---|---|---|')
for name, (out, bc) in res.items():
    allh = sum(r > 0 for r, _ in out.values())
    ch = statistics.mean(c for _, c in out.values())
    print(f'| {name} | {bc} | {cell(out,"ru","ru")} | {cell(out,"ru","en")} | {cell(out,"en","ru")} | {cell(out,"en","en")} | {allh}/{len(out)} | {ch:.0f} |')
json.dump({k: {f'{t}|{q}': v for (t, q), v in o.items()} for k, (o, _) in res.items()}, open(S + '/variants-result.json', 'w'))
