"""Model-free #831 measurement; run with the Hermes Python and a built memcue."""
import argparse
import inspect
import json
import os
from pathlib import Path
from random import Random
import statistics
import subprocess
import tempfile
import time
from unittest.mock import patch

from test_request_context import OPEN, USER, bare, card, henv, install


def bench(binary, samples):
    with tempfile.TemporaryDirectory(prefix='memcue-snapshot-') as tmp:
        home, env = henv(tmp)
        env['MEMCUE_FACTS'] = str(db := home / 'facts.db')
        with patch.dict(os.environ, env, clear=True), patch.object(tempfile, 'tempdir', tmp):
            install(home, binary)
            return measure(binary, home, db, samples)


def measure(binary, home, db, samples):
    from agent.runtime_cwd import clear_session_cwd, set_session_cwd
    from agent.transports.codex import ResponsesApiTransport
    from hermes_cli.middleware import apply_llm_request_middleware
    from hermes_cli.plugins import get_plugin_manager
    from plugins.memory import load_memory_provider

    def cli(*args, **kwargs):
        return subprocess.run([binary, *args], text=True, capture_output=True, check=True, **kwargs).stdout

    repos = {}
    facts = {}
    batch = []
    for name, count in (('empty', 0), ('large', 195), ('stress', 1950)):
        repo = repos[name] = home / name
        subprocess.run(['git', 'init', '-q', str(repo)], check=True)
        subprocess.run(['git', '-C', str(repo), 'remote', 'add', 'origin', f'https://example.invalid/{name}.git'], check=True)
        facts[name] = [f'Generator fixture {i:04d} fails when the gate stays closed, because the worker cannot complete the initial state transition. On a clean checkout open the gate before starting the worker and leave it open until the state file reports the transition.' for i in range(count)]
        batch.extend({'content': line, 'kind': 'pitfall', 'scope': f'repo:example.invalid/{name}', 'source': 'tool'} for line in facts[name])
    seed = home / 'fixture.json'
    seed.write_text(json.dumps(batch))
    cli('add', '-batch', str(seed))
    assert db.is_file()
    repos['outside'] = home
    facts['outside'] = []

    get_plugin_manager().discover_and_load(force=True)
    provider = load_memory_provider('memcue')
    assert provider is not None and hasattr(provider, '_run')
    provider.initialize('bench', hermes_home=str(home), cwd=str(home), agent_context='primary')
    bridge = provider._run
    transport = ResponsesApiTransport()
    flags = {'sanitize_harmony_tokens': True} if 'sanitize_harmony_tokens' in inspect.signature(transport.preflight_kwargs).parameters else {}
    codex = {'model': 'gpt-6.1-sol', 'instructions': 'Fixture.', 'input': USER['messages'], 'store': False}
    chat = {'model': 'synthetic', **USER}

    def send(name, request=chat, *, unavailable=False):
        key = 'input' if 'input' in request else 'messages'
        set_session_cwd(str(repos[name]))
        try:
            with patch.object(provider, '_run', wraps=bridge) as calls:
                start = time.perf_counter_ns()
                out = apply_llm_request_middleware(request, session_id='bench', turn_id='one-turn', api_mode='codex_responses' if key == 'input' else 'chat_completions').payload
                elapsed = time.perf_counter_ns() - start
                assert calls.call_count == 1 and calls.call_args.args[0] == ['session-card', '-pitfalls-json']
        finally:
            clear_session_cwd()
        text = card(out, key)
        assert text is not None
        if unavailable:
            assert 'unavailable' in text
            delivered = []
        else:
            assert 'supersedes earlier' in text.lower() and 'unavailable' not in text
            delivered = [line for line in text.splitlines() if line.startswith('Generator fixture ')]
        assert sorted(delivered) == sorted([] if unavailable else facts[name])
        original = bare(request, key)
        assert bare(out, key) == original
        # Exercise the released native Responses shape path and compact JSON independently of the timing.
        if key == 'input':
            checked = transport.preflight_kwargs(out, allow_stream=False, **flags)
            encoded = json.dumps(checked)
            assert all(line in encoded for line in delivered)
            assert checked['instructions'] == original['instructions']
        wire_size = lambda p: len(json.dumps(p, ensure_ascii=False, separators=(',', ':')).encode())
        return out, {'us': elapsed / 1000, 'processes': calls.call_count, 'bytes': wire_size(out)-wire_size(original)}

    # Same synthesized SQLite base for all measurement rows. Warm every fixture first.
    for name in repos:
        send(name)
    order = list(repos) * samples
    Random(831).shuffle(order)
    rows = {name: [] for name in repos}
    for name in order:
        rows[name].append(send(name)[1])
    for name in ('large', 'stress'):
        sent, size = send(name, codex)
        assert send(name, sent)[0] == sent
        print(json.dumps({'preflight': name, 'facts': len(facts[name]), 'bytes_added': size['bytes']}))

    populated, _ = send('large')
    empty, _ = send('empty', populated)
    outside, _ = send('outside', populated)
    assert empty != populated and outside != populated
    assert OPEN not in json.dumps(bare(empty, 'messages'))
    outside_card = card(outside, 'messages')
    assert isinstance(outside_card, str) and 'supersedes earlier' in outside_card.lower()

    # Guaranteed new fact at the same cwd; a cwd-only cache would still serve the old snapshot.
    changed = 'Generator fixture 9999 fails without the extra gate. Open the extra gate before starting the worker.'
    cli('add', '-cwd', str(repos['large']), '-kind', 'pitfall', '-source', 'tool', '-scope', 'repo', '--', changed)
    facts['large'].append(changed)
    refreshed, _ = send('large', populated)
    refreshed_card = card(refreshed, 'messages')
    assert isinstance(refreshed_card, str) and refreshed != populated and changed in refreshed_card

    # Force a real native open failure without changing production code or the data.
    backup = db.with_suffix('.saved')
    db.rename(backup)
    db.write_bytes(b'Not a SQLite database. Synthetic failure.')
    try:
        failed, _ = send('large', refreshed, unavailable=True)
    finally:
        db.unlink()
        backup.rename(db)
    assert send('large', failed)[0] == refreshed
    # Dropping an empty card altogether would erase this explicit invalidation.
    assert 'supersedes earlier' not in json.dumps(bare(outside, 'messages')).lower()

    for name, measured in rows.items():
        elapsed = [r['us'] / 1000 for r in measured]
        assert len(set(r['bytes'] for r in measured)) == 1
        print(json.dumps({'case': name, 'n': len(measured), 'p50_ms': round(statistics.median(elapsed), 3), 'p95_ms': round(statistics.quantiles(elapsed, n=100, method='inclusive')[94], 3), 'bytes_added': measured[0]['bytes'], 'processes': sum(r['processes'] for r in measured)}))
    print(json.dumps({'controls': 'retry, cwd, mutation, read failure, recovery, no-card invalidation'}))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--samples', type=int, default=50)
    args = parser.parse_args()
    if args.samples < 2:
        parser.error('--samples must be at least 2')
    bench(str(Path(os.environ['MEMCUE_BIN']).resolve(strict=True)), args.samples)
