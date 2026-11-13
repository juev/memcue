#!/usr/bin/env python3
"""The before/after measurement of #756: the text one memcue_search answer costs the model in OpenCode.

Two arms, one prompt, one model, one facts base and one binary; only the plugin's rendering differs:

- before: the plugin of `--before-ref` (main), which hands the model `JSON.stringify(answer, null, 2)`;
- after: the plugin of `--after-ref` (the branch), which hands it `renderSearch` from memcue-transport.

The metric is `tokens.input + tokens.cache.read` of the step that follows the tool result, read from the
`step_finish` events of `opencode run --format json`. The prompt asks for exactly one search, so every counted run
has the same conversation apart from the tool result; a run that deviates or fails is not counted.

Run it from anywhere; it builds a throwaway root under --workdir:

    python3 docs/research/scripts/toolrendering756/measure.py \
        --repo . --opencode ~/bin/opencode --workdir /private/tmp/m756-measure --runs 3

Nothing here touches a live memory base: the facts are synthetic and live in the throwaway root, `MEMCUE_SERVER`
and the other remote settings are cleared for the child, and the plugin gets `MEMCUE_AUTO_INSTALL=0`.

Requires an OpenCode with a working model and `--standalone` (2.2.7 or later): the isolated home gets a copy of the
machine's `auth.json` (mode 0600) so the same model answers, and the checkout's own dependencies are only read
(node_modules is symlinked, not copied).
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

PROMPT = ('Make exactly one memcue_search call: query "quarry pipeline checks", limit 5. Do not search again. '
          'Then reply with the id and version of every fact you received, one line each, and stop.')
FACTS = [
    'The Quarry release pipeline runs make test, then the checksum step, and only after both pass does the release '
    'workflow publish the tag and the archives to the mirror.',
    'Quarry pipeline checks run on two hosts and take about four minutes together; the slower host is the one that '
    'runs the integration suite against a throwaway Postgres instance.',
    'A failing Quarry pipeline check names the failing job in its last line, which is what the release notes quote, '
    'so the job name has to stay stable across releases.',
    'Quarry pipeline checks are skipped for a documentation-only change, and the release then waits for a maintainer '
    'to approve the skip before the tag is created.',
    'A Quarry pipeline check that times out is retried once before the release stops; a second timeout fails the '
    'release and never retries the archive upload.',
]


def run(cmd, **kwargs):
    return subprocess.run(cmd, text=True, capture_output=True, **kwargs)


def build_root(repo: Path, workdir: Path):
    binary = Path(tempfile.mkdtemp(prefix='bin-', dir=workdir)) / 'memcue'
    built = run(['go', 'build', '-trimpath', '-o', str(binary), './cmd/memcue'], cwd=repo)
    if built.returncode != 0:
        sys.exit(f'go build failed:\n{built.stderr}')
    return binary


def seed(base: Path, binary: Path, repo: Path):
    base.mkdir(parents=True, exist_ok=True)
    env = {key: value for key, value in os.environ.items() if not key.startswith('MEMCUE_')}
    env['MEMCUE_FACTS'] = str(base)
    for fact in FACTS:
        added = run([str(binary), 'add', '-source', 'user', '-kind', 'rule', '-scope', 'global', '--', fact],
                    cwd=repo, env=env, stdin=subprocess.DEVNULL)
        if added.returncode != 0:
            sys.exit(f'memcue add failed:\n{added.stderr}')
    return env


def arm_sources(repo: Path, ref: str, target: Path, plugin: Path):
    """The plugin package of `ref`: everything of plugins/opencode, its src replaced by that ref's."""
    source = target / f'plugins-{ref}'
    shutil.copytree(plugin, source, ignore=shutil.ignore_patterns('node_modules'))
    (source / 'node_modules').symlink_to(plugin / 'node_modules', target_is_directory=True)
    src = source / 'src'
    shutil.rmtree(src, ignore_errors=True)
    src.mkdir()
    for name in run(['git', 'ls-tree', '--name-only', f'{ref}:plugins/opencode/src'], cwd=repo).stdout.split():
        blob = run(['git', 'show', f'{ref}:plugins/opencode/src/{name}'], cwd=repo).stdout
        (src / name).write_text(blob, encoding='utf-8')
    return source


def copy_auth(home: Path, source: Path):
    """The machine's OpenCode credentials, into the isolated home: the same model answers both arms."""
    target = home / '.local' / 'share' / 'opencode' / 'auth.json'
    target.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, target)
    target.chmod(0o600)


def config_home(home: Path, plugin_dir: Path, binary: Path, base: Path, model: str):
    config = home / '.config' / 'opencode' / 'opencode.json'
    config.parent.mkdir(parents=True, exist_ok=True)
    config.write_text(json.dumps({
        '$schema': 'https://opencode.ai/config.json',
        'share': 'disabled',
        'model': model,
        'plugins': [{'package': str(plugin_dir),
                     'options': {'binary': str(binary), 'facts': str(base), 'host': 'rendering-756'}}],
    }, indent=2) + '\n', encoding='utf-8')


def measure(env, args, home: Path, repo_dir: Path):
    environ = dict(env)
    environ.update(HOME=str(home), XDG_DATA_HOME=str(home / '.local/share'), XDG_CONFIG_HOME=str(home / '.config'),
                   XDG_STATE_HOME=str(home / '.local/state'), XDG_CACHE_HOME=str(home / '.cache'),
                   MEMCUE_AUTO_INSTALL='0')
    completed = subprocess.run([args.opencode, 'run', '--standalone', '--auto', '--format', 'json', PROMPT],
                               cwd=repo_dir, env=environ, text=True, capture_output=True, timeout=args.timeout)
    steps, tool_result, tool_calls, failed = [], None, 0, False
    for line in completed.stdout.splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue
        if event.get('type') == 'step_finish':
            tokens = event['part']['tokens']
            steps.append(tokens['input'] + tokens.get('cache', {}).get('read', 0))
        if event.get('type') == 'tool_use' and event['part'].get('tool') == 'execute':
            tool_calls += 1
            tool_result = event['part'].get('state', {}).get('output')
        if event.get('type') == 'error':
            failed = True
    if failed or tool_calls != 1 or tool_result is None or len(steps) < 2:
        return None
    return {'tool_result_chars': len(tool_result), 'tool_result_lines': tool_result.count('\n') + 1,
            'step2_prompt_tokens': steps[1]}


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument('--repo', default='.', help='the checkout whose plugin is the after arm')
    parser.add_argument('--before-ref', default='main', help='the ref that carries the pretty-printed JSON')
    parser.add_argument('--after-ref', default='HEAD', help='the ref that carries the rendering')
    parser.add_argument('--opencode', default=shutil.which('opencode') or 'opencode')
    parser.add_argument('--model', default='opencode/iceman-free')
    parser.add_argument('--runs', type=int, default=3)
    parser.add_argument('--timeout', type=int, default=180)
    parser.add_argument('--workdir', default=None)
    parser.add_argument('--auth-from', default=None,
                        help='the OpenCode auth.json to copy into the isolated home '
                             '(default: $XDG_DATA_HOME/opencode/auth.json of this machine)')
    parser.add_argument('--out', default=None, help='where to write the results JSON')
    args = parser.parse_args()

    repo = Path(args.repo).resolve()
    workdir = Path(args.workdir or tempfile.mkdtemp(prefix='memcue-756-')).resolve()
    workdir.mkdir(parents=True, exist_ok=True)
    home = workdir / 'home'
    home.mkdir(exist_ok=True)
    repo_dir = workdir / 'repo'
    if not repo_dir.exists():
        repo_dir.mkdir()
        run(['git', 'init', '-q', str(repo_dir)])
    plugin = repo / 'plugins' / 'opencode'

    data_home = Path(os.environ.get('XDG_DATA_HOME') or Path.home() / '.local/share')
    auth = Path(args.auth_from) if args.auth_from else data_home / 'opencode' / 'auth.json'
    if auth.exists():
        copy_auth(home, auth)
    else:
        print(f'no credentials at {auth}: OpenCode may have no model to run', file=sys.stderr)
    binary = build_root(repo, workdir)
    base = workdir / 'facts'
    env = seed(base, binary, repo)
    arms = {}
    for arm, ref in (('before', args.before_ref), ('after', args.after_ref)):
        arms[arm] = arm_sources(repo, ref, workdir, plugin)

    results = {'prompt': PROMPT, 'model': args.model, 'opencode': args.opencode,
               'before_ref': args.before_ref, 'after_ref': args.after_ref, 'arms': {}}
    for arm, plugin_dir in arms.items():
        config_home(home, plugin_dir, binary, base, args.model)
        runs = []
        for attempt in range(1, args.runs * 3 + 1):
            if len(runs) >= args.runs:
                break
            try:
                measured = measure(env, args, home, repo_dir)
            except subprocess.TimeoutExpired:
                print(f'{arm}: run {attempt} timed out, not counted', file=sys.stderr)
                continue
            if measured is None:
                print(f'{arm}: run {attempt} deviated from one search, not counted', file=sys.stderr)
                continue
            runs.append(measured)
            print(f"{arm}: {measured['step2_prompt_tokens']} tokens, {measured['tool_result_chars']} chars",
                  file=sys.stderr)
        results['arms'][arm] = {'package': str(plugin_dir), 'runs': runs}

    def median(values):
        ordered = sorted(values)
        middle = len(ordered) // 2
        return (ordered[middle - 1] + ordered[middle]) / 2 if len(ordered) % 2 == 0 else ordered[middle]

    before = [run['step2_prompt_tokens'] for run in results['arms']['before']['runs']]
    after = [run['step2_prompt_tokens'] for run in results['arms']['after']['runs']]
    if before and after:
        results['medians'] = {'before': median(before), 'after': median(after)}
        results['medians']['delta_tokens'] = median(before) - median(after)
    text = json.dumps(results, indent=2, ensure_ascii=False) + '\n'
    if args.out:
        Path(args.out).write_text(text, encoding='utf-8')
    else:
        print(text)


if __name__ == '__main__':
    main()
