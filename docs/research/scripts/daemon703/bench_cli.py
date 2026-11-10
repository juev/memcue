#!/usr/bin/env python3
"""Time memcue CLI calls the way hooks make them (#703).

Each call is a new process, as today: search, session-start and pre-command, 10 queries x REPS, from a synthetic
repository whose origin is one of the generated repositories. Prints one JSON line per call and a summary.

    python3 -I bench_cli.py LABEL WRAPPER REPO_DIR [REPS] > out.jsonl

WRAPPER is an executable that runs memcue with the backend under test (mc-text.sh, mc-pg.sh, or a daemon client).
Wall time is measured around /usr/bin/time, whose maximum resident set size is the child's peak RSS (macOS -l).
"""
import json
import re
import statistics
import subprocess
import sys
import time

QUERIES = ['deploy release tag pipeline', 'cache directory stale worker lock', 'certificate proxy restart chain',
           'migration transaction schema rows', 'health check timeout node', 'index rebuild production writes',
           'сборка кэш воркер лок', 'деплой тег релиза артефакт', 'таймаут проверки здоровья узел',
           'launcher explicit base path state directory']


def run(wrapper, args, cwd, data=None):
    start = time.monotonic()
    r = subprocess.run(['/usr/bin/time', '-l', wrapper, *args], cwd=cwd, input=data, capture_output=True,
                       text=True, timeout=60)
    took = (time.monotonic() - start) * 1000
    rss = re.search(r'(\d+)\s+maximum resident set size', r.stderr)
    row = {'ms': round(took, 1), 'rc': r.returncode, 'rss_mb': round(int(rss.group(1)) / 2**20, 1) if rss else None,
           'out_bytes': len(r.stdout), 'err': r.stderr.split('\n', 1)[0][:200] if r.returncode else ''}
    # What the answer held, so arms can be compared and an empty answer is not mistaken for a fast one.
    try:
        answer = json.loads(r.stdout.splitlines()[-1]) if r.stdout.strip() else {}
    except ValueError:
        answer = {}
    if isinstance(answer.get('facts'), list):
        row['ids'] = [f['id'] for f in answer['facts']]
    context = answer.get('hookSpecificOutput', answer)
    if 'repositoryPitfallCount' in context:
        row['pitfalls'] = context['repositoryPitfallCount']
    return row


def main():
    label, wrapper, repo = sys.argv[1], sys.argv[2], sys.argv[3]
    reps = int(sys.argv[4]) if len(sys.argv) > 4 else 3
    rows = []
    for rep in range(reps):
        for i, q in enumerate(QUERIES):
            session = f'bench-{label}-{rep}-{i}'
            calls = {
                'search': (['search', '-json', '-in', 'all', q], None),
                'session-start': (['session-start'], json.dumps({'session_id': session, 'cwd': repo,
                                                                 'source': 'startup', 'hook_event_name': 'SessionStart'})),
                'pre-command': (['pre-command', '-cwd', repo, '-session', session, '-action', 'build', '--', q], None),
            }
            for name, (args, data) in calls.items():
                row = {'label': label, 'cmd': name, 'rep': rep, 'q': i, **run(wrapper, args, repo, data)}
                rows.append(row)
                print(json.dumps(row, ensure_ascii=False), flush=True)
    for name in ('search', 'session-start', 'pre-command'):
        ms = sorted(r['ms'] for r in rows if r['cmd'] == name)
        rss = [r['rss_mb'] for r in rows if r['cmd'] == name and r['rss_mb']]
        bad = sum(1 for r in rows if r['cmd'] == name and r['rc'])
        print(json.dumps({'summary': label, 'cmd': name, 'n': len(ms), 'median_ms': statistics.median(ms),
                          'p95_ms': ms[max(0, int(len(ms) * 0.95) - 1)], 'max_rss_mb': max(rss) if rss else None,
                          'failed': bad}), file=sys.stderr, flush=True)


if __name__ == '__main__':
    main()
