#!/usr/bin/env python3
"""Answer quality of the #756 search rendering: does a model use memory as well from the compact line as from JSON?

Two arms of the OpenCode plugin, the same binary, the same synthetic base and the same tasks:

- json: the plugin of `--json-ref` (main), which hands the model `JSON.stringify(answer, null, 2)`;
- compact: the plugin of `--compact-ref` (the branch), one `id@version scope YYYY-MM-DD content` line per fact.

Each run is one `opencode run --standalone` session on a fresh copy of the base, so a correction or a retirement of
one run never reaches the next. A task is graded twice where it can be:

- by the base: what the run left behind (a corrected version, a retired fact, no duplicate), and by the tool calls
  (a shelf search after the shelf hint);
- by a judge: one `claude -p` call per task reads the seeded facts, the reference answer and every answer of that
  task in a shuffled order, without the arm or the model, and scores each 0, 1 or 2.

    python3 docs/research/scripts/toolrendering756/quality.py run --repo . --workdir /private/tmp/q756 \\
        --models opencode/iceman-free,openai/gpt-5.6-luna --reps 3 --jobs 4
    python3 docs/research/scripts/toolrendering756/quality.py judge --workdir /private/tmp/q756
    python3 docs/research/scripts/toolrendering756/quality.py report --workdir /private/tmp/q756 --out results.json

Nothing here touches a live memory base: the facts are synthetic and live in the throwaway root, every `MEMCUE_*`
variable of the caller is dropped for the child, and the plugin gets `MEMCUE_AUTO_INSTALL=0`. The isolated home
gets a copy of the machine's OpenCode `auth.json` (mode 0600) so the named models answer.
"""

import argparse
import concurrent.futures
import json
import os
import random
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ORBIT = 'example.invalid/acme/orbit'
COMET = 'example.invalid/acme/comet'

# key, scope, kind, day written, content. The base the model searches; "this repository" is Orbit.
FACTS = [
    ('port', f'repo:{ORBIT}', 'environment', '2026-10-02',
     "Orbit's staging PostgreSQL listens on port 6543; the local docker-compose database stays on 5432."),
    ('comet-port', f'repo:{COMET}', 'environment', '2026-10-05',
     "Comet's staging PostgreSQL listens on port 7001."),
    ('commits', 'global', 'rule', '2026-09-10',
     'Commit messages are written in English, with a ticket prefix taken from the git remote.'),
    ('nightly', f'repo:{ORBIT}', 'decision', '2026-11-02',
     'Orbit publishes no nightly Docker images any more; only tagged releases build images (decided by the user).'),
    ('ci-old', f'repo:{ORBIT}', 'environment', '2025-03-04',
     "Orbit's CI runs on Jenkins at ci.orbit.example.invalid."),
    ('ci-new', f'repo:{ORBIT}', 'environment', '2026-10-20',
     "Orbit's CI runs on GitHub Actions with the workflow .github/workflows/ci.yml."),
    ('pitfall', f'repo:{ORBIT}', 'pitfall', '2026-10-15',
     "Orbit's integration tests fail with 'too many clients' when run with -p 8; run them with -p 2."),
    ('lumen', f'repo:{ORBIT}', 'environment', '2026-08-21',
     'The Lumen cron job on orbit-worker-1 rotates the audit logs every night at 03:00.'),
    ('zephyr', 'shelf:research', 'environment', '2026-10-30',
     'Zephyr object storage limits a single PUT to 5 GiB; larger uploads must use multipart with at most 10,000 '
     'parts. Source: Zephyr API reference, read 2026-10-30.'),
    ('sentry', f'repo:{COMET}', 'environment', '2026-09-28',
     "Comet's Sentry project is comet-backend and its alerts go to the #comet-alerts channel."),
]

# An isolated home has no catalogue of the API-key providers, and OpenCode then answers with its own free models
# instead of the named one (served_by catches that); these entries describe the providers the measurement names.
PROVIDERS = {
    'deepseek': {'provider': {'deepseek': {
        'npm': '@ai-sdk/openai-compatible', 'name': 'DeepSeek',
        'options': {'baseURL': 'https://api.deepseek.com/v1'},
        'models': {'deepseek-chat': {'name': 'DeepSeek Chat', 'tool_call': True}}}}},
    'zai-coding-plan': {'provider': {'zai-coding-plan': {
        'npm': '@ai-sdk/openai-compatible', 'name': 'Z.ai Coding Plan',
        'options': {'baseURL': 'https://api.z.ai/api/coding/paas/v4'},
        'models': {'glm-4.6': {'name': 'GLM-4.6', 'tool_call': True}}}}},
}

PREFACE = 'Check long-term memory with the memcue tools before you answer. Answer in at most three sentences. '

# id, prompt, reference for the judge, deterministic check (None when only the judge grades).
TASKS = [
    ('read-port', 'What port does the staging database of this repository listen on?',
     '6543 (the local docker-compose database is 5432; 7001 is Comet, another repository).', None),
    ('read-decision', 'Does this repository still publish nightly Docker images?',
     'No: only tagged releases build images; nightly images were stopped (decision of 2026-11-02).', None),
    ('read-pitfall', "The integration tests here fail with 'too many clients'. What should I do?",
     'Run them with -p 2 instead of -p 8.', None),
    ('stale', 'Memory may hold two notes about where the CI of this repository runs. Where does it run now, and why '
              'do you think so?',
     'GitHub Actions (.github/workflows/ci.yml): that fact was written 2026-10-20, the Jenkins one 2025-03-04, so '
     'Jenkins is the older and likely stale note. Full credit needs the dates (or explicit recency) as the reason.',
     None),
    ('correct', "Update memory: the staging database of this repository moved to port 6600. Correct the stored fact "
                "rather than adding a new one.",
     'The model corrects the existing port fact to 6600 (keeping the 5432 local note is a plus) and says so.',
     'correct'),
    ('retire', 'The Lumen cron job on orbit-worker-1 was removed. Remove that fact from memory.',
     'The model retires the Lumen fact and says so; nothing else changes.', 'retire'),
    ('shelf', 'What is the largest single PUT that Zephyr object storage accepts? Use memory, including research '
              'findings.',
     '5 GiB; larger uploads need multipart with at most 10,000 parts. The finding is on the shelf: an ordinary '
     'search returns nothing and names -in shelf:research.', 'shelf'),
    ('widened', 'Which Sentry project and alert channel does this repository use?',
     'Memory holds no Sentry fact for this repository (Orbit); the only one is for Comet, another repository '
     '(comet-backend, #comet-alerts). Full credit: says it is from another repository and does not claim it for '
     'Orbit. Zero: presents comet-backend as this repository\'s project.', None),
    ('absent', 'Who is on call for this repository this week?',
     'Memory holds nothing about an on-call rotation; the model says so and does not invent names.', None),
]


def run(cmd, **kwargs):
    return subprocess.run(cmd, text=True, capture_output=True, **kwargs)


def clean_env():
    return {key: value for key, value in os.environ.items() if not key.startswith('MEMCUE_')}


def build_binary(repo: Path, workdir: Path) -> Path:
    binary = workdir / 'bin' / 'memcue'
    binary.parent.mkdir(parents=True, exist_ok=True)
    built = run(['go', 'build', '-trimpath', '-o', str(binary), './cmd/memcue'], cwd=repo)
    if built.returncode != 0:
        sys.exit(f'go build failed:\n{built.stderr}')
    return binary


def seed(template: Path, binary: Path, scratch: Path):
    """The template base: every fact added through the CLI, then its `created` set to the day of the table."""
    env = clean_env()
    env.update(MEMCUE_FACTS=str(template), HOME=str(scratch), XDG_STATE_HOME=str(scratch / 'state'),
               MEMCUE_DREAM_BACKGROUND='0', MEMCUE_AUTO_INSTALL='0')
    ids = {}
    for key, scope, kind, day, content in FACTS:
        origin = f"{scope.removeprefix('repo:') if scope.startswith('repo:') else 'example.invalid/notes'} {day}"
        added = run([str(binary), 'add', '-source', 'user', '-kind', kind, '-scope', scope, '-origin', origin,
                     '-cwd', '', '--', content], env=env, stdin=subprocess.DEVNULL)
        if added.returncode != 0:
            sys.exit(f'memcue add failed for {key}:\n{added.stderr}')
        ids[key] = added.stdout.strip()
    for path in template.rglob('*.jsonl'):
        rows = [json.loads(line) for line in path.read_text(encoding='utf-8').splitlines() if line.strip()]
        key = next(k for k, v in ids.items() if v == rows[0]['id'])
        day = next(f[3] for f in FACTS if f[0] == key)
        for row in rows:
            row['created'] = f'{day}T10:00:00Z'
        path.write_text(''.join(json.dumps(row, ensure_ascii=False) + '\n' for row in rows), encoding='utf-8')
    return ids


def arm_package(repo: Path, ref: str, target: Path) -> Path:
    """The plugin package of `ref`: plugins/opencode of the checkout, its src replaced by that ref's."""
    plugin = repo / 'plugins' / 'opencode'
    source = target / f'plugin-{ref.replace("/", "_")}'
    if source.exists():
        return source
    shutil.copytree(plugin, source, ignore=shutil.ignore_patterns('node_modules'))
    (source / 'node_modules').symlink_to(plugin / 'node_modules', target_is_directory=True)
    src = source / 'src'
    shutil.rmtree(src)
    src.mkdir()
    for name in run(['git', 'ls-tree', '--name-only', f'{ref}:plugins/opencode/src'], cwd=repo).stdout.split():
        blob = run(['git', 'show', f'{ref}:plugins/opencode/src/{name}'], cwd=repo).stdout
        (src / name).write_text(blob, encoding='utf-8')
    return source


def one_run(job):
    """One session: a fresh home, a fresh copy of the base, the task prompt; the events and the base it left."""
    root = Path(job['root'])
    if (root / 'done.json').exists():
        return json.loads((root / 'done.json').read_text())
    shutil.rmtree(root, ignore_errors=True)
    home, facts, project = root / 'home', root / 'facts', root / 'orbit'
    shutil.copytree(job['template'], facts)
    project.mkdir(parents=True)
    run(['git', 'init', '-q', str(project)])
    run(['git', '-C', str(project), 'remote', 'add', 'origin', f'https://{ORBIT}.git'])
    auth = home / '.local' / 'share' / 'opencode' / 'auth.json'
    auth.parent.mkdir(parents=True)
    shutil.copyfile(job['auth'], auth)
    auth.chmod(0o600)
    config = home / '.config' / 'opencode' / 'opencode.json'
    config.parent.mkdir(parents=True)
    config.write_text(json.dumps({
        '$schema': 'https://opencode.ai/config.json', 'share': 'disabled', 'model': job['model'],
        'small_model': job['model'], **PROVIDERS.get(job['model'].split('/')[0], {}),
        'plugins': [{'package': job['package'],
                     'options': {'binary': job['binary'], 'facts': str(facts), 'host': 'quality-756'}}],
    }, indent=2) + '\n', encoding='utf-8')
    env = clean_env()
    env.update(HOME=str(home), XDG_DATA_HOME=str(home / '.local/share'), XDG_CONFIG_HOME=str(home / '.config'),
               XDG_STATE_HOME=str(home / '.local/state'), XDG_CACHE_HOME=str(home / '.cache'),
               MEMCUE_AUTO_INSTALL='0', MEMCUE_DREAM_BACKGROUND='0')
    try:
        done = subprocess.run([job['opencode'], 'run', '--standalone', '--auto', '--format', 'json', '--model', job['model'],
                               job['prompt']],
                              cwd=project, env=env, text=True, capture_output=True, timeout=job['timeout'],
                              stdin=subprocess.DEVNULL)
        stdout, code = done.stdout, done.returncode
    except subprocess.TimeoutExpired as expired:
        stdout, code = expired.stdout or '', 'timeout'
        if isinstance(stdout, bytes):
            stdout = stdout.decode('utf-8', 'replace')
    (root / 'events.jsonl').write_text(stdout, encoding='utf-8')
    events = [json.loads(line) for line in stdout.splitlines() if line.startswith('{')]
    answer, calls, errors = [], [], []
    for event in events:
        part = event.get('part') or {}
        if event.get('type') == 'text' and part.get('text'):
            answer.append(part['text'])
        if event.get('type') == 'tool_use':
            state = part.get('state') or {}
            calls.append({'tool': part.get('tool'), 'input': state.get('input'),
                          'output': str(state.get('output') or state.get('error') or '')[:4000]})
        if event.get('type') == 'error':
            errors.append(str(event.get('error') or event)[:500])
    result = {'task': job['task'], 'arm': job['arm'], 'model': job['model'], 'rep': job['rep'], 'exit': code,
              'answer': '\n'.join(answer).strip(), 'calls': calls, 'errors': errors, 'base': base_state(facts),
              'served_by': served_by(home)}
    (root / 'done.json').write_text(json.dumps(result, ensure_ascii=False, indent=1), encoding='utf-8')
    return result


def served_by(home: Path):
    """The models that actually answered, from the session store of the run: OpenCode falls back to its own free
    models when the configured one is not used, and a run is only counted when every answer came from the model it
    names."""
    import sqlite3
    models = set()
    for db in home.rglob('opencode.db'):
        connection = sqlite3.connect(f'file:{db}?mode=ro', uri=True)
        try:
            for (data,) in connection.execute('select data from session_message'):
                model = (json.loads(data) or {}).get('model') or {}
                if model.get('id'):
                    models.add(f"{model.get('providerID')}/{model['id']}")
        finally:
            connection.close()
    return sorted(models)


def base_state(facts: Path):
    """Every fact of the run's base after it: the last version of each id."""
    state = {}
    for path in facts.rglob('*.jsonl'):
        rows = [json.loads(line) for line in path.read_text(encoding='utf-8').splitlines() if line.strip()]
        last = max(rows, key=lambda row: row['version'])
        state[last['id']] = {'version': last['version'], 'status': last['status'], 'content': last['content']}
    return state


def check(result, ids):
    """The deterministic grade of a task, or None when only the judge grades it."""
    task = next(t for t in TASKS if t[0] == result['task'])
    base = result['base']
    kind = task[3]
    if kind is None:
        return None
    others_intact = all(base.get(ids[key], {}).get('version') == 1 and base[ids[key]]['status'] == 'active'
                        for key in ids if key not in ('port', 'lumen'))
    if kind == 'correct':
        port = base.get(ids['port'], {})
        active = sum(1 for fact in base.values() if fact['status'] == 'active')
        return {'pass': port.get('status') == 'active' and port.get('version', 1) >= 2 and '6600' in port['content']
                and active == len(FACTS) and others_intact,
                'version': port.get('version'), 'content': port.get('content'), 'active': active,
                'others_intact': others_intact}
    if kind == 'retire':
        lumen = base.get(ids['lumen'], {})
        port = base.get(ids['port'], {})
        active = sum(1 for fact in base.values() if fact['status'] == 'active')
        return {'pass': lumen.get('status') == 'retired' and port.get('version') == 1 and others_intact
                and active == len(FACTS) - 1, 'lumen': lumen.get('status'), 'active': active,
                'others_intact': others_intact}
    if kind == 'shelf':
        searched = any('shelf' in json.dumps(call.get('input') or {}) for call in result['calls'])
        return {'pass': searched, 'shelf_search': searched}
    raise ValueError(kind)


def unasked_changes(result, ids):
    """The facts a question-only task changed: a correction or a retirement the user never asked for."""
    if result['task'] in ('correct', 'retire'):
        return []
    names = {value: key for key, value in ids.items()}
    return sorted(f"{names.get(fact_id, fact_id)}:{fact['status']}@{fact['version']}"
                  for fact_id, fact in result['base'].items() if fact['version'] > 1 or fact['status'] != 'active')


def session_tokens(workdir: Path, result):
    """The model steps of the session and the input side they sent, from the run's step_finish events."""
    events = workdir / 'runs' / result['model'].replace('/', '_') / result['task'] / f"{result['arm']}-{result['rep']}"
    steps = []
    for line in (events / 'events.jsonl').read_text(encoding='utf-8').splitlines():
        event = json.loads(line) if line.startswith('{') else {}
        if event.get('type') == 'step_finish':
            tokens = event['part']['tokens']
            steps.append(tokens['input'] + tokens.get('cache', {}).get('read', 0))
    return {'steps': len(steps), 'input_tokens': sum(steps)}


def cmd_run(args):
    repo = Path(args.repo).resolve()
    workdir = Path(args.workdir).resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    binary = build_binary(repo, workdir)
    template = workdir / 'template'
    if not template.exists():
        ids = seed(template, binary, Path(tempfile.mkdtemp(prefix='seed-', dir=workdir)))
        (workdir / 'ids.json').write_text(json.dumps(ids, indent=1), encoding='utf-8')
    data_home = Path(os.environ.get('XDG_DATA_HOME') or Path.home() / '.local/share')
    auth = Path(args.auth_from) if args.auth_from else data_home / 'opencode' / 'auth.json'
    packages = {'json': arm_package(repo, args.json_ref, workdir), 'compact': arm_package(repo, args.compact_ref,
                                                                                          workdir)}
    tasks = [t for t in TASKS if not args.tasks or t[0] in args.tasks.split(',')]
    jobs = []
    for model in args.models.split(','):
        for task in tasks:
            for arm, package in packages.items():
                for rep in range(1, args.reps + 1):
                    jobs.append({'root': str(workdir / 'runs' / model.replace('/', '_') / task[0] / f'{arm}-{rep}'),
                                 'template': str(template), 'auth': str(auth), 'binary': str(binary),
                                 'package': str(package), 'opencode': args.opencode, 'model': model,
                                 'task': task[0], 'arm': arm, 'rep': rep, 'prompt': PREFACE + task[1],
                                 'timeout': args.timeout})
    random.Random(756).shuffle(jobs)
    with concurrent.futures.ThreadPoolExecutor(max_workers=args.jobs) as pool:
        for result in pool.map(one_run, jobs):
            print(f"{result['model']} {result['task']} {result['arm']}-{result['rep']}: exit {result['exit']}, "
                  f"{len(result['calls'])} calls, {len(result['answer'])} chars", file=sys.stderr, flush=True)


def valid(result):
    """A run counts when it finished, no provider error came, and every answer came from the model it names."""
    return (result['exit'] == 0 and not result['errors'] and bool(result['answer'])
            and result.get('served_by') == [result['model']])


def results_of(workdir: Path, everything=False):
    results = [json.loads(path.read_text()) for path in sorted((workdir / 'runs').rglob('done.json'))]
    return results if everything else [result for result in results if valid(result)]


JUDGE_SCHEMA = {
    'type': 'object', 'additionalProperties': False, 'required': ['scores'],
    'properties': {'scores': {'type': 'array', 'items': {
        'type': 'object', 'additionalProperties': False, 'required': ['label', 'score', 'reason'],
        'properties': {'label': {'type': 'string'}, 'score': {'type': 'integer', 'enum': [0, 1, 2]},
                       'reason': {'type': 'string'}}}}},
}


def cmd_judge(args):
    workdir = Path(args.workdir).resolve()
    results = results_of(workdir)
    facts = '\n'.join(f'- [{scope}, written {day}] {content}' for _, scope, _, day, content in FACTS)
    judged = {}
    out = workdir / 'judged.json'
    if out.exists():
        judged = json.loads(out.read_text())
    for task_id, prompt, reference, _ in TASKS:
        if task_id in judged:
            continue
        rows = [r for r in results if r['task'] == task_id]
        if not rows:
            continue
        random.Random(task_id).shuffle(rows)
        labels = {f'A{i + 1:02d}': row for i, row in enumerate(rows)}
        answers = '\n\n'.join(f'### {label}\n{row["answer"] or "(no answer)"}' for label, row in labels.items())
        brief = (
            'You grade answers of a coding assistant that has a long-term memory tool. The memory held exactly these '
            'facts (scope, day written, content); "this repository" is example.invalid/acme/orbit (Orbit):\n'
            f'{facts}\n\nThe user asked:\n{PREFACE}{prompt}\n\nReference: {reference}\n\n'
            'Score every answer below: 2 = correct and complete per the reference, 1 = partly right or right but '
            'missing what the reference requires for full credit, 0 = wrong, invented, or no answer. Judge only the '
            'content of the answer, not its style or length. Return one entry per label.\n\n' + answers)
        env = {k: v for k, v in os.environ.items() if not k.startswith('MEMCUE_')}
        done = run([args.claude, '-p', '--model', args.judge_model, '--output-format', 'json', '--tools', '',
                    '--mcp-config', '{"mcpServers":{}}', '--strict-mcp-config',
                    '--json-schema', json.dumps(JUDGE_SCHEMA), brief], env=env, stdin=subprocess.DEVNULL,
                   timeout=900)
        if done.returncode != 0:
            sys.exit(f'judge failed on {task_id}:\n{done.stderr[-2000:]}\n{done.stdout[-2000:]}')
        envelope = json.loads(done.stdout)
        scores = envelope.get('structured_output') or json.loads(envelope['result'])
        judged[task_id] = {'cost_usd': envelope.get('total_cost_usd'), 'scores': {
            entry['label']: {'score': entry['score'], 'reason': entry['reason'],
                             'run': [labels[entry['label']][k] for k in ('model', 'arm', 'rep')]}
            for entry in scores['scores'] if entry['label'] in labels}}
        missing = set(labels) - set(judged[task_id]['scores'])
        if missing:
            sys.exit(f'judge skipped {sorted(missing)} on {task_id}')
        out.write_text(json.dumps(judged, ensure_ascii=False, indent=1), encoding='utf-8')
        print(f'judged {task_id}: {len(labels)} answers, ${judged[task_id]["cost_usd"]}', file=sys.stderr)


def cmd_report(args):
    workdir = Path(args.workdir).resolve()
    ids = json.loads((workdir / 'ids.json').read_text())
    judged = json.loads((workdir / 'judged.json').read_text()) if (workdir / 'judged.json').exists() else {}
    score = {}
    for task_id, entry in judged.items():
        for value in entry['scores'].values():
            model, arm, rep = value['run']
            score[(task_id, model, arm, rep)] = value
    rows = []
    every = results_of(workdir, everything=True)
    dropped = [{k: r[k] for k in ('task', 'model', 'arm', 'rep', 'exit', 'errors', 'served_by')}
               for r in every if not valid(r)]
    for result in results_of(workdir):
        key = (result['task'], result['model'], result['arm'], result['rep'])
        rows.append({'task': result['task'], 'model': result['model'], 'arm': result['arm'], 'rep': result['rep'],
                     'exit': result['exit'], 'calls': len(result['calls']),
                     'searches': sum(1 for c in result['calls'] if 'memcue_search' in json.dumps(c)),
                     'judge': score.get(key, {}).get('score'), 'judge_reason': score.get(key, {}).get('reason'),
                     'check': check(result, ids), 'unasked_changes': unasked_changes(result, ids),
                     **session_tokens(workdir, result), 'answer': result['answer']})
    summary = {}
    for row in rows:
        cell = summary.setdefault(row['model'], {}).setdefault(row['task'], {}).setdefault(
            row['arm'], {'judge': [], 'check': []})
        if row['judge'] is not None:
            cell['judge'].append(row['judge'])
        if row['check'] is not None:
            cell['check'].append(row['check']['pass'])
    text = json.dumps({'issue': 756, 'facts': FACTS, 'preface': PREFACE, 'tasks': TASKS,
                       'judge_cost_usd': sum((e.get('cost_usd') or 0) for e in judged.values()),
                       'summary': summary, 'runs': rows, 'not_counted': dropped}, ensure_ascii=False, indent=1) + '\n'
    if args.out:
        Path(args.out).write_text(text, encoding='utf-8')
    for model, tasks in summary.items():
        print(f'\n{model}')
        for task_id, arms in tasks.items():
            cells = []
            for arm in ('json', 'compact'):
                cell = arms.get(arm, {'judge': [], 'check': []})
                judge = f"{sum(cell['judge'])}/{2 * len(cell['judge'])}" if cell['judge'] else '-'
                passed = f"{sum(cell['check'])}/{len(cell['check'])}" if cell['check'] else '-'
                cells.append(f'{arm}: judge {judge} check {passed}')
            print(f'  {task_id:14} ' + ' | '.join(cells))


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    sub = parser.add_subparsers(dest='command', required=True)
    p_run = sub.add_parser('run')
    p_run.add_argument('--repo', default='.')
    p_run.add_argument('--workdir', required=True)
    p_run.add_argument('--json-ref', default='main')
    p_run.add_argument('--compact-ref', default='HEAD')
    p_run.add_argument('--opencode', default=shutil.which('opencode') or 'opencode')
    p_run.add_argument('--models', default='opencode/iceman-free')
    p_run.add_argument('--tasks', default='')
    p_run.add_argument('--reps', type=int, default=3)
    p_run.add_argument('--jobs', type=int, default=4)
    p_run.add_argument('--timeout', type=int, default=300)
    p_run.add_argument('--auth-from', default=None)
    p_judge = sub.add_parser('judge')
    p_judge.add_argument('--workdir', required=True)
    p_judge.add_argument('--claude', default=shutil.which('claude') or 'claude')
    p_judge.add_argument('--judge-model', default='claude-opus-5-5')
    p_report = sub.add_parser('report')
    p_report.add_argument('--workdir', required=True)
    p_report.add_argument('--out', default=None)
    args = parser.parse_args()
    {'run': cmd_run, 'judge': cmd_judge, 'report': cmd_report}[args.command](args)


if __name__ == '__main__':
    main()
