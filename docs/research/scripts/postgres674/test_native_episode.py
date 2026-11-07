"""Controlled native-protocol fixtures; never authenticated model evidence."""
import json
import contextlib
import ctypes
import os
from pathlib import Path
import signal
import subprocess
import tempfile
from unittest import mock
import sys
import time
import unittest
from native_episode import ProcessSearch, run_episode


def call(n=1, tool='memory_search'):
    return {'id': n, 'method': 'item/tool/call', 'params': {'threadId': 'thread',
        'turnId': 'turn', 'callId': str(n), 'tool': tool, 'namespace': None,
        'arguments': {'query': 'model-selected fixture query'}}}


def finish(units=12):
    events = [{'method': 'item/completed', 'params': {'threadId': 'thread',
        'turnId': 'turn', 'item': {'type': 'agentMessage', 'text': 'Synthetic fixture final.', 'phase': 'final_answer'}}}]
    if units is not None:
        events.append({'method': 'codex/event/token_count', 'params': {'msg': {'type': 'token_count',
            'info': {'total_token_usage': {'output_tokens': 12, 'codex_rollout_budget_units': units}}}}})
    events.append({'method': 'turn/completed', 'params': {'threadId': 'thread',
        'turn': {'id': 'turn', 'status': 'completed'}}})
    return events


class Client:
    def __init__(self, events):
        self.events = list(events); self.closed = 0; self.interrupted = 0
        self.submitted = 0; self.replies = []
    def start(self, deadline):
        self.submitted += 1; return 'thread', 'turn'
    def next(self, deadline):
        if self.events: return self.events.pop(0)
        time.sleep(max(0, deadline-time.monotonic())+.005)
        raise TimeoutError('fixture idle deadline')
    def reply(self, id, value): self.replies.append((id, value))
    def interrupt(self, thread, turn): self.interrupted += 1
    def close(self): self.closed += 1; return {'exit': 0}


class EpisodeTests(unittest.TestCase):
    def search(self, args, deadline):
        return {'hits': [], 'search_executed': True}

    def test_genuine_protocol_dispatch_empty_retry_then_final(self):
        c = Client([call(1), call(2), *finish()]); seen = []
        def search(a, d): seen.append(a); return self.search(a, d)
        r = run_episode(c, search)
        self.assertTrue(r['eligible']); self.assertEqual(r['executed_searches'], 2)
        self.assertEqual(c.submitted, 1); self.assertEqual(len(c.replies), 2)
        self.assertEqual(len(seen), 2); self.assertEqual(c.closed, 1)

    def test_fourth_search_never_executes_and_is_ineligible(self):
        c = Client([call(i) for i in range(1, 5)]+finish()); seen = []
        r = run_episode(c, lambda a,d: seen.append(a) or self.search(a,d))
        self.assertFalse(r['eligible']); self.assertEqual(r['executed_searches'], 3)
        self.assertEqual(len(seen), 3); self.assertEqual(c.interrupted, 1)

    def test_unexpected_tool_duplicate_and_malformed_event(self):
        for events in [[call(tool='exec')], [call(), call()], [None]]:
            with self.subTest(events=len(events)):
                c = Client(events); r = run_episode(c, self.search)
                self.assertFalse(r['eligible']); self.assertEqual(c.closed, 1)
                self.assertEqual(c.interrupted, 1)

    def test_no_event_deadline_interrupts_and_closes(self):
        c = Client([]); start = time.monotonic()
        r = run_episode(c, self.search, deadline_seconds=.05)
        self.assertFalse(r['eligible']); self.assertTrue(r['deadline_exceeded'])
        self.assertLess(time.monotonic()-start, 1); self.assertEqual(c.interrupted, 1)
        self.assertEqual(c.closed, 1)

    def test_process_timeout_nonzero_malformed_flow_into_eligibility(self):
        scripts = ["import time; time.sleep(60)", "raise SystemExit(23)", "print('not JSON')"]
        for script in scripts:
            with self.subTest(script=script[:15]):
                c = Client([call(), *finish()]); start = time.monotonic()
                r = run_episode(c, ProcessSearch([sys.executable, '-c', script]), deadline_seconds=.2)
                self.assertFalse(r['eligible']); self.assertEqual(c.closed, 1)
                self.assertLess(time.monotonic()-start, 2)

    def test_malformed_known_event_cannot_be_masked_by_later_answer(self):
        malformed = [
            {'method': 'item/completed', 'params': {'threadId': 'thread', 'turnId': 'turn', 'item': {}}},
            {'method': 'thread/tokenUsage/updated', 'params': {'threadId': 'thread', 'turnId': 'turn', 'tokenUsage': 'malformed'}},
        ]
        for event in malformed:
            c = Client([call(), event, *finish()])
            r = run_episode(c, self.search)
            self.assertFalse(r['eligible']); self.assertEqual(c.closed, 1)

    def test_usage_unknown_and_overrun_remain_ineligible(self):
        for units, status in [(None, 'UNKNOWN'), (4097, 'OVERRUN')]:
            c = Client([call(), *finish(units)])
            r = run_episode(c, self.search)
            self.assertFalse(r['eligible']); self.assertEqual(r['budget']['status'], status)

    def test_successful_subprocess_response_is_actual_json(self):
        script = "import json; print(json.dumps({'hits': [], 'search_executed': True}))"
        c = Client([call(), *finish()])
        r = run_episode(c, ProcessSearch([sys.executable, '-c', script]))
        self.assertTrue(r['eligible']); self.assertEqual(r['executed_searches'], 1)


@contextlib.contextmanager
def test_subreaper():
    libc = ctypes.CDLL(None, use_errno=True)
    previous = ctypes.c_int()
    if libc.prctl(37, ctypes.byref(previous), 0, 0, 0): raise OSError(ctypes.get_errno())
    if libc.prctl(36, 1, 0, 0, 0): raise OSError(ctypes.get_errno())
    try: yield
    finally:
        if libc.prctl(36, previous.value, 0, 0, 0): raise OSError(ctypes.get_errno())


def process_identity(pid):
    try:
        fields = Path('/proc/'+str(pid)+'/stat').read_text().rsplit(')', 1)[1].split()
        return {'pid': pid, 'state': fields[0], 'pgid': int(fields[2]), 'sid': int(fields[3]), 'start': fields[19]}
    except FileNotFoundError: return None


FORK_FIXTURE = r"""import json,os,signal,sys,time
mode,ready=sys.argv[1:]
r,w=os.pipe()
pid=os.fork()
if pid==0:
 os.close(r)
 if mode!='exited':signal.signal(signal.SIGTERM,signal.SIG_IGN)
 fields=open('/proc/self/stat').read().rsplit(')',1)[1].split()
 with open(ready,'x') as f:json.dump({'pid':os.getpid(),'pgid':os.getpgrp(),'sid':os.getsid(0),'start':fields[19]},f)
 if mode in ('nonzero','success'):
  for fd in [0,1,2]:os.close(fd)
 os.write(w,b'1');os.close(w)
 time.sleep(5);os._exit(0)
os.close(w);os.read(r,1);os.close(r)
if mode=='exited':os._exit(0)
if mode=='nonzero':os._exit(23)
if mode=='success':
 print(json.dumps({'hits':[],'search_executed':True}),flush=True);os._exit(0)
signal.signal(signal.SIGTERM,lambda *_:os._exit(0))
time.sleep(5);os._exit(0)
"""


class ProcessLifetimeTests(unittest.TestCase):
    def probe(self, mode):
        base = os.environ.get('PG674_LIFETIME_EVIDENCE_DIR')
        manager = contextlib.nullcontext(Path(base)/mode) if base else tempfile.TemporaryDirectory()
        with manager as directory, test_subreaper():
            root = Path(directory)
            if base: root.mkdir(mode=0o700)
            fixture = root/'fixture.py'; fixture.write_text(FORK_FIXTURE); fixture.chmod(0o600)
            ready = root/'ready.json'; proof = {'mode': mode}; child = None
            try:
                c = Client([call(), *finish()]); start = time.monotonic()
                search = ProcessSearch([sys.executable, '-B', str(fixture), mode, str(ready)])
                result = run_episode(c, search, deadline_seconds=.15)
                self.assertTrue(ready.is_file(), 'deterministic fork synchronization must complete')
                child = json.loads(ready.read_text()); current = process_identity(child['pid'])
                proof.update(result=result, elapsed=time.monotonic()-start,
                             descendant_present_after_return=current is not None, descendant_identity=child)
                self.assertFalse(result['eligible'])
                self.assertEqual(c.closed, 1)
                self.assertLess(proof['elapsed'], 2)
                self.assertIsNone(current, 'owned descendant must be terminated AND reaped before return')
                self.assertTrue(result['backend_processes'][0].get('pipe_drain', {}).get('complete', False), 'owned output pipes must reach EOF after group cleanup')
                if mode=='success': self.assertEqual(c.replies, [], 'cleanup must not publish a successful search after the external deadline')
                if mode=='nonzero':
                    self.assertEqual(result['error']['type'], 'SearchFailure')
                    self.assertEqual(result['error']['reason'], 'backend nonzero exit')
                else: self.assertEqual(result['error']['type'], 'TimeoutError')
            finally:
                if child is None and ready.is_file(): child = json.loads(ready.read_text())
                if child:
                    current = process_identity(child['pid'])
                    if current and current['start']==child['start']:
                        fd = os.pidfd_open(child['pid'])
                        try: signal.pidfd_send_signal(fd, signal.SIGKILL)
                        finally: os.close(fd)
                    deadline = time.monotonic()+1
                    while time.monotonic()<deadline:
                        try:
                            got,_ = os.waitpid(child['pid'], os.WNOHANG)
                            if got: break
                        except ChildProcessError: break
                        time.sleep(.005)
                    proof['descendant_absent_after_test_finally'] = process_identity(child['pid']) is None
                if base:
                    with (root/'proof.json').open('x') as f:json.dump(proof,f,indent=2)

    def test_exited_leader_child_holds_pipes_top_level(self): self.probe('exited')
    def test_term_exits_active_leader_but_child_ignores_term(self): self.probe('active')
    def test_nonzero_error_preserved_after_descendant_cleanup(self): self.probe('nonzero')
    def test_cleanup_grace_cannot_publish_result_after_deadline(self): self.probe('success')

    def test_ordinary_completion_does_not_signal_unrelated_group(self):
        with test_subreaper():
            unrelated = subprocess.Popen([sys.executable, '-c', 'import time;time.sleep(5)'], start_new_session=True)
            try:
                script = "import json;print(json.dumps({'hits': [], 'search_executed': True}))"
                search = ProcessSearch([sys.executable, '-c', script])
                with mock.patch('native_episode.os.killpg', wraps=os.killpg) as signals:
                    result = run_episode(Client([call(), *finish()]), search)
                self.assertTrue(result['eligible'])
                self.assertIsNone(os.waitid(os.P_PID, unrelated.pid, os.WEXITED|os.WNOHANG|os.WNOWAIT))
                self.assertTrue(all(c.args[0]!=unrelated.pid for c in signals.call_args_list))
                self.assertLess(result['elapsed_seconds'], 2)
            finally:
                unrelated.kill(); unrelated.wait(timeout=1)


if __name__ == '__main__': unittest.main()
