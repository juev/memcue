#!/usr/bin/env python3
"""Run the existing memcue CLI with one explicit Codex configuration."""
import argparse
import hashlib
import json
import os
import re
import secrets
from pathlib import Path
import shlex
import subprocess as _subprocess
import sys
import tempfile
import time

try:
    import fcntl
except ImportError:  # Windows is not a supported platform; snapshots are then compared without a lock.
    fcntl = None


class _ResponseTransportError(OSError):
    attempted = True


class _OwnedSubprocess:
    """One POSIX session per invocation; keep its leader unreaped until cleanup.

    Descendants that deliberately create another session (including the persistent
    tsnet helper) are outside this invocation's group. No global reaper is used.
    """
    def __getattr__(self, name):
        return getattr(_subprocess, name)

    def run(self, args, *, input=None, capture_output=False, text=False,
            encoding=None, errors=None, timeout=None, check=False, **kwargs):
        if os.name != 'posix' or not hasattr(os, 'WNOWAIT'):
            return _subprocess.run(args, input=input, capture_output=capture_output,
                                   text=text, encoding=encoding, errors=errors,
                                   timeout=timeout, check=check, **kwargs)
        import signal
        import time
        from contextlib import ExitStack
        attempted = False
        try:
            start = time.monotonic()
            deadline = None if timeout is None else start + timeout
            with ExitStack() as stack:
                source = stack.enter_context(tempfile.TemporaryFile())
                if input is not None:
                    source.write(input.encode(encoding or 'utf-8', errors or 'strict') if isinstance(input, str) else input)
                source.seek(0)
                outputs = []
                if capture_output:
                    for key in ('stdout', 'stderr'):
                        f = stack.enter_context(tempfile.TemporaryFile())
                        kwargs[key] = f
                        outputs.append(f)
                if deadline is not None and time.monotonic() >= deadline:
                    raise TimeoutError('memcue budget expired before executable start')
                proc = _subprocess.Popen(args, stdin=source, start_new_session=True, **kwargs)
                attempted = True
                timed_out = False
                try:
                    while True:
                        # WNOWAIT leaves the leader's PID reserved, even when it is
                        # a zombie. Never signal a group after reaping its anchor.
                        status = os.waitid(os.P_PID, proc.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                        if status is not None:
                            break
                        if deadline is not None and time.monotonic() >= deadline - min(.02, timeout / 10):
                            timed_out = True
                            break
                        time.sleep(.002)
                finally:
                    # Confirm that our child is still waitable before signalling.
                    # If another component reaped it, do not signal a recycled PID.
                    os.waitid(os.P_PID, proc.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
                    try:
                        os.killpg(proc.pid, signal.SIGKILL)
                    except (ProcessLookupError, PermissionError):
                        # macOS reports EPERM instead of ESRCH when only the
                        # unreaped leader of our own group remains.
                        pass
                    proc.wait(timeout=None if deadline is None else max(.001, deadline - time.monotonic()))
                if timed_out:
                    raise _subprocess.TimeoutExpired(args, timeout)
                values = []
                for f in outputs:
                    f.seek(0)
                    chunks = []
                    while True:
                        if deadline is not None and time.monotonic() >= deadline:
                            raise _subprocess.TimeoutExpired(args, timeout)
                        chunk = f.read(65536)
                        if not chunk:
                            break
                        chunks.append(chunk)
                    b = b''.join(chunks)
                    values.append(b.decode(encoding or 'utf-8', errors or 'strict') if text or encoding else b)
                    if deadline is not None and time.monotonic() >= deadline:
                        raise _subprocess.TimeoutExpired(args, timeout)
                result = _subprocess.CompletedProcess(args, proc.returncode, *(values or [None, None]))
                if check:
                    result.check_returncode()
                return result
        except OSError as exc:
            if attempted:
                raise _ResponseTransportError('memcue response transport unavailable') from exc
            raise

subprocess = _OwnedSubprocess()

ROOT = Path(__file__).resolve().parents[1]
LIMIT = 4 * 1024 * 1024
INPUT_LIMIT = 64 * 1024
TIMEOUT = 15


class AdapterError(Exception):
    """A failure the caller can report without stopping Codex."""


def bounded(text):
    return ' '.join(str(text).split())[:600]


def load_config(path):
    if not path.is_absolute():
        raise AdapterError('configuration path must be absolute')
    with path.open('rb') as f:
        data = f.read(INPUT_LIMIT + 1)
    if len(data) > INPUT_LIMIT:
        raise AdapterError('configuration exceeds 64 KiB')
    config = json.loads(data)
    allowed = {'binary', 'facts', 'state_dir', 'host', 'background_dream', 'dream_every'}
    if not isinstance(config, dict) or config.keys() - allowed:
        raise AdapterError('configuration must be an object with documented keys only')
    for key in ('binary', 'facts', 'state_dir'):
        value = config.get(key)
        if not isinstance(value, str) or not value or not Path(value).is_absolute():
            raise AdapterError(key + ' must be an absolute path')
    if not Path(config['binary']).is_file() or not os.access(config['binary'], os.X_OK):
        raise AdapterError('configured memcue binary is missing or not executable')
    host = config.get('host')
    if not isinstance(host, str) or not host or any(c.isspace() for c in host):
        raise AdapterError('host must be an explicit nonempty name without whitespace')
    if config.get('background_dream', False) is not False:
        raise AdapterError('background_dream must be false; this adapter uses model-driven dream only')
    if 'dream_every' in config and (not isinstance(config['dream_every'], str) or not config['dream_every']):
        raise AdapterError('dream_every must be a nonempty Go duration, for example 24h')
    return config


def child_env(config):
    env = {k: v for k, v in os.environ.items() if not k.startswith('MEMCUE_')}
    # Explicit backend/transport selection survives this boundary. Removing the
    # DSN would silently redirect a remote invocation into the configured local base.
    for key in ('MEMCUE_POSTGRES_DSN', 'MEMCUE_SERVER', 'MEMCUE_TAILSCALE_MODE',
                'MEMCUE_TAILSCALE_STATE_DIR', 'MEMCUE_TAILSCALE_HOSTNAME',
                'MEMCUE_TAILSCALE_CONTROL_URL', 'MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL',
                'MEMCUE_TAILSCALE_CONNECT_TIMEOUT'):
        if key in os.environ:
            env[key] = os.environ[key]
    env.update(MEMCUE_FACTS=config['facts'], MEMCUE_STATE_DIR=config['state_dir'],
               MEMCUE_HOST=config['host'], MEMCUE_DREAM_BACKGROUND='0')
    if 'dream_every' in config:
        env['MEMCUE_DREAM_EVERY'] = config['dream_every']
    return env


def unknown_mutation(args):
    dream = bool(args and args[0] == 'dream' and not any(v.lstrip('-').split('=', 1)[0] == 'list' for v in args[1:]))
    if not args or args[0] not in ('add', 'correct', 'retire', 'reopen') and not dream:
        return ''
    batch = args[0] == 'add' and any(v.lstrip('-').split('=', 1)[0] == 'batch' for v in args[1:])
    if batch:
        return ('UNKNOWN OUTCOME for the entire batch; response incomplete. Inspect durable facts and history '
                'for every batch row before deciding whether to retry; do not automatically replay the batch.')
    if dream:
        return ('UNKNOWN OUTCOME for the local plan; it may be partially applied. Inspect durable history '
                'for every affected fact ID before deciding whether to retry; do not automatically replay the plan.')
    fact_id = ''
    if args[0] in ('correct', 'retire', 'reopen'):
        i = 1
        target = ''
        while i < len(args):
            value = args[i]
            if value == '--':
                target = args[i + 1] if i + 1 < len(args) else ''
                break
            if not value.startswith('-'):
                target = value
                break
            flag = value.lstrip('-').split('=', 1)[0]
            if flag in ('expected-version', 'expires'):
                if '=' not in value:
                    i += 1
            elif flag != 'no-expires':
                break
            i += 1
        if re.fullmatch(r'[0-9A-HJKMNP-TV-Z]{26}', target):
            fact_id = ' for fact ' + target
    return ('UNKNOWN OUTCOME' + fact_id + '; mutation response unavailable. Inspect durable ID/history '
            'with memcue history before deciding whether to retry; do not automatically replay.')


def classify_outcome(output, diagnostic, code, nonce):
    marker = '\nMEMCUE_OUTCOME_V1 '
    at = diagnostic.rfind(marker)
    if at < 0 or not diagnostic.endswith('\n'):
        return 'unknown', ''
    try:
        receipt = json.loads(diagnostic[at + len(marker):-1])
    except (ValueError, TypeError):
        return 'unknown', ''
    if not isinstance(receipt, dict):
        return 'unknown', ''
    if (receipt.get('version') != 1 or receipt.get('nonce') != nonce or
            receipt.get('bytes') != len(output.encode('utf-8')) or
            receipt.get('sha256') != hashlib.sha256(output.encode('utf-8')).hexdigest()):
        return 'unknown', ''
    status = receipt.get('status')
    if status == 'success' and code == 0:
        return 'success', ''
    if status == 'refused' and code > 0:
        prelude = diagnostic[:at]
        if (receipt.get('error_bytes') == len(prelude.encode('utf-8')) and
                receipt.get('error_sha256') == hashlib.sha256(prelude.encode('utf-8')).hexdigest()):
            return 'refused', prelude.strip()
        return 'refused', 'memcue refused the operation before any fact change; check input, authentication and observed version'
    return 'unknown', ''






def call(config, args, cwd, data='', timeout=TIMEOUT):
    nonce = secrets.token_hex(16) if unknown_mutation(args) else ''
    env = child_env(config)
    if nonce:
        env['MEMCUE_OUTCOME_NONCE'] = nonce
    # Files avoid unbounded in-memory capture from a broken binary. Time and read size are bounded.
    with tempfile.TemporaryFile() as out, tempfile.TemporaryFile() as err:
        try:
            result = subprocess.run([config['binary'], *args], input=data.encode(), stdout=out, stderr=err,
                                    cwd=cwd, env=env, timeout=timeout)
        except subprocess.TimeoutExpired as exc:
            raise AdapterError(unknown_mutation(args) or 'memcue command timed out; check persisted state before retrying a write') from exc
        except OSError as exc:
            if getattr(exc, 'attempted', False):
                raise AdapterError(unknown_mutation(args) or 'memcue response transport unavailable') from exc
            raise
        try:
            out.seek(0)
            output = out.read(LIMIT + 1)
            err.seek(0)
            diagnostic = err.read(LIMIT + 1).decode('utf-8', errors='replace')
        except OSError as exc:
            raise AdapterError(unknown_mutation(args) or 'memcue response transport unavailable') from exc
        if nonce:
            status, refusal = classify_outcome(output.decode('utf-8', errors='replace'), diagnostic, result.returncode, nonce)
            if status == 'unknown':
                raise AdapterError(unknown_mutation(args))
            if status == 'refused':
                raise AdapterError('memcue command failed (exit ' + str(result.returncode) + '): ' + bounded(refusal))
        elif result.returncode:
            raise AdapterError('memcue command failed (exit ' + str(result.returncode) + '): ' + bounded(diagnostic))
        if len(output) > LIMIT:
            raise AdapterError(unknown_mutation(args) or 'memcue output exceeds the supported 4 MiB transport; complete delivery is unsupported, no subset delivered')
        return output.decode('utf-8')


def hook_input():
    raw = sys.stdin.buffer.read(INPUT_LIMIT + 1)
    if len(raw) > INPUT_LIMIT:
        raise AdapterError('hook input exceeds 64 KiB')
    data = json.loads(raw)
    if not isinstance(data, dict) or data.get('hook_event_name') not in ('SessionStart', 'UserPromptSubmit', 'PreToolUse'):
        raise AdapterError('expected SessionStart hook input')
    if data['hook_event_name'] == 'SessionStart' and data.get('source') not in ('startup', 'resume', 'clear', 'compact', 'fork'):
        raise AdapterError('unsupported SessionStart source')
    for key in ('session_id', 'cwd', 'model', 'permission_mode'):
        if not isinstance(data.get(key), str) or not data[key]:
            raise AdapterError('hook input needs ' + key)
    if data['permission_mode'] not in ('default', 'acceptEdits', 'plan', 'dontAsk', 'bypassPermissions'):
        raise AdapterError('invalid permission_mode')
    if 'transcript_path' not in data or not (data['transcript_path'] is None or isinstance(data['transcript_path'], str)):
        raise AdapterError('hook input needs nullable transcript_path')
    if data['hook_event_name'] == 'PreToolUse':
        tool = data.get('tool_input')
        if data.get('tool_name') != 'Bash' or not isinstance(tool, dict):
            raise AdapterError('expected Bash tool input')
        target = tool.get('workdir') or tool.get('cwd')
        if target:
            if not isinstance(target, str):
                raise AdapterError('tool cwd must be a path')
            data['cwd'] = os.path.abspath(Path(data['cwd']) / target)
            data['tool_input'] = {k:v for k,v in tool.items() if k not in ('workdir', 'cwd')}
    cwd = Path(data['cwd'])
    if not cwd.is_absolute() or not cwd.is_dir():
        raise AdapterError('hook cwd must name an existing absolute directory')
    return data


def context(command):
    return (ROOT / 'policy.md').read_text(encoding='utf-8').replace('@MEMCUE@', command)


SNAPSHOTS = 'codex-pitfall-snapshots.json'
SNAPSHOT_SESSIONS = 128


def snapshot_changed(config, session, text):
    """Say whether text differs from the last repository-pitfall snapshot this session received (#705).

    PreToolUse runs before every shell command, and an unchanged snapshot only repeats what the context holds.
    text None forgets the session: after startup, resume, clear or compaction the context may not hold it any more.
    A state that cannot be read or written delivers the snapshot rather than risk withholding a changed one.
    """
    state = Path(config['state_dir'])
    key = hashlib.sha256(session.encode('utf-8')).hexdigest()
    digest = None if text is None else hashlib.sha256(text.encode('utf-8')).hexdigest()
    try:
        state.mkdir(parents=True, exist_ok=True)
        with open(state / (SNAPSHOTS + '.lock'), 'a', encoding='utf-8') as lock:
            if fcntl:
                fcntl.flock(lock, fcntl.LOCK_EX)
            path = state / SNAPSHOTS
            try:
                seen = json.loads(path.read_text(encoding='utf-8'))
            except (FileNotFoundError, ValueError):
                seen = {}
            if not isinstance(seen, dict):
                seen = {}
            entry = seen.pop(key, None)
            if digest is not None and isinstance(entry, dict) and entry.get('sha256') == digest:
                return False
            if digest is not None:
                seen[key] = {'sha256': digest, 'at': time.time()}
            if len(seen) > SNAPSHOT_SESSIONS:
                oldest = sorted(seen, key=lambda k: seen[k].get('at', 0) if isinstance(seen[k], dict) else 0)
                for old in oldest[:len(seen) - SNAPSHOT_SESSIONS]:
                    del seen[old]
            tmp = path.with_name(path.name + '.' + secrets.token_hex(8))
            tmp.write_text(json.dumps(seen), encoding='utf-8')
            os.replace(tmp, path)
    except OSError:
        return True
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    default = os.environ.get('MEMCUE_CODEX_CONFIG') or str(Path(os.environ.get('CODEX_HOME', str(Path.home()/'.codex'))) / 'memcue.json')
    parser.add_argument('--config', default=default, help='absolute path to memcue JSON configuration')
    parser.add_argument('command')
    parser.add_argument('args', nargs=argparse.REMAINDER)
    args = parser.parse_args()
    hook = args.command == 'hook'
    response = {}
    try:
        config_path = Path(args.config)
        config = load_config(config_path)
        command = shlex.join([sys.executable, str(Path(__file__).resolve()), '--config', str(config_path)])
        if hook:
            response['hookSpecificOutput'] = {'hookEventName': 'SessionStart', 'additionalContext': context(command)}
            data = hook_input()
            event = data['hook_event_name']
            response['hookSpecificOutput'] = {'hookEventName': event, 'additionalContext': context(command) if event == 'SessionStart' else ''}
            call(config, ['version', '-minimum', '0.30.1'], data['cwd'], timeout=5)
            flags = ['-dream-tools'] if event == 'SessionStart' else ['-pitfalls-only']
            card = call(config, ['session-card', *flags], data['cwd'], json.dumps(data), timeout=10)
            if card.strip():
                output = json.loads(card)['hookSpecificOutput']
                if not isinstance(output, dict) or output.get('hookEventName') != ('SessionCard' if event == 'SessionStart' else event) or not isinstance(output.get('additionalContext'), str):
                    raise AdapterError('invalid memcue SessionCard response')
                hints = output['additionalContext'].replace(
                    'call memcue_dream_prepare and answer its packet with memcue_dream_apply',
                    'run the memcue-dream skill with the configured launcher')
                response['hookSpecificOutput']['additionalContext'] += '\n\n' + hints
            elif event != 'SessionStart':
                response['hookSpecificOutput']['additionalContext'] = 'Repository pitfalls: no git repository. This supersedes earlier repository-pitfall snapshots.'
            if event == 'SessionStart':
                snapshot_changed(config, data['session_id'], None)
            elif not snapshot_changed(config, data['session_id'], response['hookSpecificOutput']['additionalContext']):
                response['hookSpecificOutput']['additionalContext'] = ''
        elif args.command == 'instructions':
            sys.stdout.write((ROOT / 'AGENTS.md.snippet').read_text(encoding='utf-8').replace(
                '@MEMCUE@', command).replace('@ROOT@', str(ROOT)))
        else:
            if args.command not in ('version', 'search', 'add', 'correct', 'retire', 'reopen', 'facts', 'dump', 'dream'):
                raise AdapterError('unsupported command; use search/add/correct/retire/reopen/facts/dump/version/dream')
            if args.command == 'dream' and not (args.args in (['-prepare'], ['-prepare', '-all'], ['-list']) or
                                                len(args.args) == 3 and args.args[:2] == ['-prepare', '-since'] and
                                                args.args[2] and not args.args[2].startswith('-') or
                                                len(args.args) == 2 and args.args[0] == '-apply'):
                raise AdapterError('dream accepts only -prepare [-all | -since DATE], -list or -apply FILE; no background runner')
            call(config, ['version', '-minimum', '0.30.0'], os.getcwd())
            sys.stdout.write(call(config, [args.command, *args.args], os.getcwd(), timeout=60 if args.command == 'dream' else TIMEOUT))
    except (AdapterError, OSError, ValueError, KeyError, TypeError) as exc:
        message = 'memcue (Codex): ' + bounded(exc) + '. Other Codex work can continue.'
        print(message, file=sys.stderr)
        if not hook:
            return 1
        response['systemMessage'] = message
    if hook:
        print(json.dumps(response))
    return 0


if __name__ == '__main__':
    sys.exit(main())
