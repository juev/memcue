"""Finite synthetic app-server dispatch and external deadline supervision.

Remote model work is supervised here. Each heavy local encoding subprocess owns
its individual build-check wrapper; this controller never holds that queue lock.
"""
import contextlib
import ctypes
import json
import math
import os
from pathlib import Path
import selectors
import signal
import subprocess
import time
from native_preflight import NativeClient, MODEL, BASE, DEVELOPER, QUESTION, TOOL


class SearchFailure(RuntimeError):
    pass


@contextlib.contextmanager
def backend_subreaper():
    # Needed to reap orphaned backend grandchildren in containers whose PID 1
    # does not reap them. Restore the process-local setting; never reap siblings.
    libc = ctypes.CDLL(None, use_errno=True)
    libc.prctl.argtypes = [ctypes.c_int, ctypes.c_void_p, ctypes.c_ulong, ctypes.c_ulong, ctypes.c_ulong]
    previous = ctypes.c_int()
    if libc.prctl(37, ctypes.byref(previous), 0, 0, 0): raise OSError(ctypes.get_errno())
    if not previous.value and libc.prctl(36, ctypes.c_void_p(1), 0, 0, 0): raise OSError(ctypes.get_errno())
    try: yield
    finally:
        if not previous.value and libc.prctl(36, None, 0, 0, 0): raise OSError(ctypes.get_errno())


def process_group_identity(pid):
    try:
        fields = Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')', 1)[1].split()
        return {'pid': pid, 'state': fields[0], 'pgid': int(fields[2]),
                'sid': int(fields[3]), 'start': fields[19]}
    except (FileNotFoundError, ProcessLookupError): return None


def owned_group(process):
    identity = process_group_identity(process.pid)
    if not identity or identity['pgid'] != process.pid or identity['sid'] != process.pid:
        raise SearchFailure('backend must own its new session/process group')
    return identity


def group_members(group):
    # Kernel pgrp/SID metadata only: no command lines, environment or file trees.
    members = []
    for entry in Path('/proc').iterdir():
        if not entry.name.isdigit(): continue
        identity = process_group_identity(int(entry.name))
        if identity and identity['pgid'] == group['pgid'] and identity['sid'] == group['sid']:
            members.append(identity)
    return members


def stop_group(process, group, grace=.15, kill_grace=.5):
    """Keep the unreaped leader as the pgid reservation until descendants vanish.

    A private session excludes unrelated joiners. Its leader's start-time identity
    is checked before every signal. No signal is sent after the anchor is reaped.
    """
    record = {'pgid': group['pgid'], 'leader_start': group['start'],
              'term_sent': False, 'kill_sent': False, 'reaped_descendants': []}
    def signal_owned(sig):
        current = process_group_identity(process.pid)
        if not current or any(current[k] != group[k] for k in ['pid', 'pgid', 'sid', 'start']):
            raise SearchFailure('owned group anchor lost; refusing signals')
        os.killpg(group['pgid'], sig)
    def reap_descendants():
        for member in group_members(group):
            if member['pid'] == process.pid: continue
            try:
                pid, _ = os.waitpid(member['pid'], os.WNOHANG)
                if pid: record['reaped_descendants'].append(pid)
            except ChildProcessError: pass  # Still parented by a living group member.
    def live_members():
        return [m for m in group_members(group) if m['state'] != 'Z']
    if live_members():
        signal_owned(signal.SIGTERM); record['term_sent'] = True
        until = time.monotonic()+grace
        while time.monotonic() < until and live_members():
            reap_descendants(); time.sleep(.005)
    if live_members():
        signal_owned(signal.SIGKILL); record['kill_sent'] = True
    until = time.monotonic()+kill_grace
    while True:
        reap_descendants()
        members = group_members(group)
        if all(m['pid'] == process.pid and m['state'] == 'Z' for m in members): break
        if time.monotonic() >= until: raise SearchFailure('owned backend group did not terminate within cleanup bound')
        time.sleep(.005)
    # Only now release the leader PID/pgid reservation. No later group signaling.
    process.wait(timeout=kill_grace)
    record['group_gone'] = not group_members(group)
    record['leader_anchor_held_until_descendants_reaped'] = True
    if not record['group_gone']: raise SearchFailure('owned backend group remains after reaping')
    return record


def drain_group_pipes(process, timeout=.05):
    # The owned writers have terminated. Verify EOF nonblocking, discarding any
    # final buffered diagnostics without retaining their contents.
    until = time.monotonic()+timeout; discarded = 0
    for stream in [process.stdout, process.stderr]:
        if stream.closed: continue
        while True:
            if time.monotonic() >= until: raise SearchFailure('backend pipe drain exceeded cleanup bound')
            try: chunk = os.read(stream.fileno(), 65536)
            except BlockingIOError:
                time.sleep(.001); continue
            if not chunk: break
            discarded += len(chunk)
    return {'complete': True, 'discarded_bytes': discarded}


def exchange_without_reaping(process, payload, deadline, clock):
    """Bound stdio and leader exit without communicate()/poll() reaping the anchor."""
    buffers = {'out': bytearray(), 'err': bytearray()}; pending = memoryview(payload)
    with selectors.DefaultSelector() as selector:
        for stream, kind in [(process.stdout, 'out'), (process.stderr, 'err'), (process.stdin, 'in')]:
            os.set_blocking(stream.fileno(), False)
            selector.register(stream, selectors.EVENT_WRITE if kind == 'in' else selectors.EVENT_READ, kind)
        while selector.get_map():
            remaining = deadline-clock()
            if remaining <= 0: raise TimeoutError('external deadline during backend subprocess')
            for key, _ in selector.select(min(.05, remaining)):
                if key.data == 'in':
                    try: written = os.write(key.fd, pending) if pending else 0
                    except BrokenPipeError: pending = memoryview(b'')
                    except BlockingIOError: continue
                    else: pending = pending[written:]
                    if not pending: selector.unregister(key.fileobj); key.fileobj.close()
                else:
                    try: chunk = os.read(key.fd, 65536)
                    except BlockingIOError: continue
                    if chunk: buffers[key.data].extend(chunk)
                    else: selector.unregister(key.fileobj); key.fileobj.close()
    while True:
        status = os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)
        if status is not None:
            exit = status.si_status if status.si_code == os.CLD_EXITED else -status.si_status
            return bytes(buffers['out']), bytes(buffers['err']), exit
        if clock() >= deadline: raise TimeoutError('external deadline waiting for backend leader')
        time.sleep(.005)


class ProcessSearch:
    def __init__(self, argv, clock=time.monotonic):
        self.argv, self.clock, self.records = list(argv), clock, []

    def __call__(self, arguments, deadline):
        if self.clock() >= deadline: raise TimeoutError('search deadline before launch')
        record = {'argv': self.argv, 'arguments': arguments, 'exit': None, 'response': None}
        self.records.append(record); start = self.clock(); process = group = None; failure = None
        with backend_subreaper():
            try:
                process = subprocess.Popen(self.argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                           stderr=subprocess.PIPE, start_new_session=True)
                group = owned_group(process); record.update(pid=process.pid, pgid=group['pgid'])
                out, err, exit = exchange_without_reaping(process, json.dumps(arguments).encode(), deadline, self.clock)
                record.update(exit=exit, stdout_bytes=len(out), stderr_bytes=len(err))
                if exit: raise SearchFailure('backend nonzero exit')
                try: result = json.loads(out)
                except (ValueError, UnicodeError): raise SearchFailure('backend malformed JSON')
                if not isinstance(result, dict) or not isinstance(result.get('hits'), list) or result.get('search_executed') is not True:
                    raise SearchFailure('backend result contract')
                record['response'] = result
                if self.clock() >= deadline: raise TimeoutError('deadline after backend completion')
                return result
            except BaseException as exc:
                failure = exc; raise
            finally:
                try:
                    if process and group:
                        try:
                            record['cleanup'] = stop_group(process, group)
                            record['pipe_drain'] = drain_group_pipes(process)
                        except Exception as exc:
                            record['cleanup_error'] = {'type': type(exc).__name__, 'reason': str(exc)}
                            if failure is None: raise
                        record['exit'] = process.returncode
                finally:
                    if process:
                        for stream in [process.stdin, process.stdout, process.stderr]:
                            if stream and not stream.closed: stream.close()
                    record['elapsed_seconds'] = self.clock()-start
                    if failure is None and 'cleanup_error' not in record and self.clock() >= deadline:
                        raise TimeoutError('external deadline after backend group cleanup')


def budget_evidence(raw_total, observed_usage):
    units = raw_total.get('codex_rollout_budget_units') if isinstance(raw_total, dict) else None
    valid = isinstance(units, (int, float)) and not isinstance(units, bool) and math.isfinite(units) and units >= 0
    status = 'UNKNOWN'
    if valid: status = 'OVERRUN' if units > 4096 else 'THRESHOLD_REACHED' if units == 4096 else 'BELOW_THRESHOLD'
    return {'status': status, 'provider_units': units if valid else None,
            'observed_native_usage': observed_usage, 'raw_total_usage': raw_total,
            'fallback_weighted_units': None,
            'qualification': 'No fallback compliance claim from a projection that may omit provider units; completed-response threshold can overshoot.'}


def run_episode(client, search, deadline_seconds=60, clock=time.monotonic):
    clock = clock or time.monotonic
    start = clock(); deadline = start+deadline_seconds
    thread = turn = None; error = None; final = None; terminal = None
    events, calls, call_ids = [], [], set()
    successful = 0; count_known = True; timed_out = False; interrupt = False
    observed_usage = raw_total = None
    cleanup = None
    try:
        thread, turn = client.start(deadline)
        while True:
            if clock() >= deadline: raise TimeoutError('external episode deadline')
            event = client.next(deadline)
            if not isinstance(event, dict) or not isinstance(event.get('method'), str):
                raise ValueError('malformed native event')
            events.append(event); method = event['method']; params = event.get('params', {})
            if not isinstance(params, dict): raise ValueError('malformed event params')
            if method == 'item/tool/call':
                if len(calls) >= 3: raise ValueError('fourth search refused')
                arguments = params.get('arguments'); call_id = params.get('callId')
                if (params.get('threadId') != thread or params.get('turnId') != turn
                    or params.get('tool') != 'memory_search' or params.get('namespace') not in (None, 'functions')
                    or not isinstance(call_id, str) or call_id in call_ids or 'id' not in event
                    or not isinstance(arguments, dict) or set(arguments) != {'query'}
                    or not isinstance(arguments['query'], str) or not arguments['query'].strip()
                    or len(arguments['query']) > 4096):
                    raise ValueError('unexpected or malformed tool call')
                call_ids.add(call_id); call = {'call_id': call_id, 'arguments': arguments, 'response': None}; calls.append(call)
                try: response = search(arguments, deadline)
                except Exception:
                    count_known = False; raise
                if response.get('search_executed') is not True or not isinstance(response.get('hits'), list):
                    count_known = False; raise SearchFailure('invalid backend response')
                successful += 1; call['response'] = response
                client.reply(event['id'], {'contentItems': [{'type': 'inputText', 'text': json.dumps({'hits': response['hits']}, ensure_ascii=False)}], 'success': True})
            elif method == 'item/completed':
                if params.get('threadId') != thread or params.get('turnId') != turn: raise ValueError('foreign item event')
                item = params.get('item', {})
                if not isinstance(item, dict) or not isinstance(item.get('type'), str) or not item['type']:
                    raise ValueError('malformed completed item')
                if item.get('type') == 'agentMessage' and item.get('phase') in (None, 'final_answer'):
                    if not isinstance(item.get('text'), str): raise ValueError('malformed agent answer')
                    final = item['text']
            elif method == 'thread/tokenUsage/updated':
                if params.get('threadId') != thread or params.get('turnId') != turn: raise ValueError('foreign usage event')
                usage = params.get('tokenUsage')
                required = {'inputTokens', 'cachedInputTokens', 'outputTokens', 'reasoningOutputTokens', 'totalTokens'}
                if not isinstance(usage, dict) or any(not isinstance(usage.get(k), dict) or not required <= set(usage[k])
                    or any(not isinstance(usage[k][f], int) or isinstance(usage[k][f], bool) or usage[k][f] < 0 for f in required)
                    for k in ['last', 'total']):
                    raise ValueError('malformed native usage')
                observed_usage = usage
            elif method == 'codex/event/token_count':
                msg = params.get('msg', {})
                info = msg.get('info') if isinstance(msg, dict) else None
                if isinstance(info, dict) and isinstance(info.get('total_token_usage'), dict):
                    raw_total = info['total_token_usage']
            elif method == 'turn/completed':
                t = params.get('turn', {})
                if params.get('threadId') != thread or not isinstance(t, dict) or t.get('id') != turn:
                    raise ValueError('foreign or malformed terminal event')
                terminal = t.get('status')
                if terminal != 'completed': raise RuntimeError('native turn not completed')
                break
            elif 'id' in event:
                raise ValueError('unexpected native server request')
    except Exception as exc:
        timed_out = isinstance(exc, TimeoutError) or clock() >= deadline
        error = {'type': type(exc).__name__, 'reason': str(exc)}
    finally:
        if error and thread and turn:
            try: client.interrupt(thread, turn); interrupt = True
            except Exception: pass
        try: cleanup = client.close()
        except Exception as exc: error = error or {'type': type(exc).__name__, 'reason': 'native cleanup failed'}
    budget = budget_evidence(raw_total, observed_usage)
    eligible = (error is None and terminal == 'completed' and bool(final) and successful >= 1
                and budget['status'] in ('BELOW_THRESHOLD', 'THRESHOLD_REACHED') and not timed_out)
    if isinstance(cleanup, dict) and (cleanup.get('exit') not in (0, -signal.SIGTERM) or cleanup.get('group_gone') is False):
        eligible = False; error = error or {'type': 'CleanupError', 'reason': 'native process lifetime failure'}
    return {'eligible': eligible, 'terminal_status': terminal, 'error': error,
            'thread_id': thread, 'turn_id': turn, 'submitted_turns': getattr(client, 'submitted_turns', getattr(client, 'submitted', 0)),
            'search_slots_used': len(calls), 'successful_searches': successful,
            'executed_searches': successful if count_known else None,
            'executed_search_upper_bound': len(calls), 'calls': calls,
            'events': events, 'final_answer': final, 'budget': budget,
            'elapsed_seconds': clock()-start, 'deadline_seconds': deadline_seconds,
            'deadline_exceeded': timed_out, 'interrupt_requested': interrupt,
            'cleanup': cleanup, 'server_generation_or_billing_stop_confirmed': False,
            'backend_processes': getattr(search, 'records', [])}


def route_argv(route, settings):
    argv = [route['binary'], 'app-server', '--strict-config', '--listen', 'stdio://']
    def flatten(prefix, value):
        if isinstance(value, dict):
            for key, item in value.items(): yield from flatten(prefix+'.'+key, item)
        else: yield prefix+'='+json.dumps(value)
    for key, value in settings.items():
        for setting in flatten(key, value): argv += ['-c', setting]
    return argv


def route_env(route):
    env = dict(route['env'])
    for key in route.get('inherit_runtime_proxy_keys', []):
        if key in os.environ: env[key] = os.environ[key]
    if route.get('ensure_loopback_no_proxy') and '127.0.0.1' not in env.get('NO_PROXY', ''):
        env['NO_PROXY'] = env.get('NO_PROXY', '')+',127.0.0.1,localhost'
    return env


class AppServerSession:
    def __init__(self, route, settings):
        self.route, self.settings = route, settings
        self.argv = route_argv(route, settings); self.submitted_turns = 0
        self.native = NativeClient(self.argv, route_env(route), route['cwd'])

    def start(self, deadline):
        self.native.rpc(1, 'initialize', {'clientInfo': {'name': 'memcue_synthetic_research', 'version': '1'}, 'capabilities': {'experimentalApi': True}}, deadline)
        self.native.send({'method': 'initialized', 'params': {}})
        r = self.native.rpc(2, 'thread/start', {'model': MODEL, 'modelProvider': 'openai',
            'cwd': self.route['cwd'], 'ephemeral': True, 'sandbox': 'read-only', 'approvalPolicy': 'never',
            'baseInstructions': BASE, 'developerInstructions': DEVELOPER, 'dynamicTools': [TOOL],
            'experimentalRawEvents': True}, deadline)
        thread = r['thread']['id']; self.submitted_turns = 1
        r = self.native.rpc(3, 'turn/start', {'threadId': thread, 'input': [{'type': 'text', 'text': QUESTION}], 'effort': 'high'}, deadline)
        return thread, r['turn']['id']

    def next(self, deadline): return self.native.next(deadline)
    def reply(self, id, result): self.native.send({'id': id, 'result': result})
    def interrupt(self, thread, turn):
        self.native.send({'id': 900, 'method': 'turn/interrupt', 'params': {'threadId': thread, 'turnId': turn}})
    def close(self):
        exit = self.native.close(); gone = False
        try: os.killpg(self.native.process.pid, 0)
        except ProcessLookupError: gone = True
        return {'exit': exit, 'pid': self.native.process.pid, 'group_gone': gone,
                'stderr_lines_discarded': len(self.native.stderr), 'credential_diagnostics_retained': False}
