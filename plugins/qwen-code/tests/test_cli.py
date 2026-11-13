"""Real memcue CLI through the Qwen Code launcher, using synthetic facts only.

Requires MEMCUE_BIN pointing to a build of this checkout. No model calls.
"""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
ADAPTER = ROOT / 'scripts' / 'memcue.py'


class RealCLI(unittest.TestCase):
    def setUp(self):
        binary = os.environ.get('MEMCUE_BIN')
        self.assertTrue(binary and Path(binary).is_file(), 'build bin/memcue and set MEMCUE_BIN; this suite must execute')
        self.tmp = tempfile.TemporaryDirectory(prefix='qwen-synthetic-')
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.project = self.home/'project'
        (self.project/'.git').mkdir(parents=True)
        (self.project/'.git/config').write_text('[remote "origin"]\nurl = https://synthetic.invalid/team/garden.git\n')
        self.nested = self.project/'a/b'
        self.nested.mkdir(parents=True)
        self.config = self.home/'memcue.json'
        self.settings = {'binary': str(Path(binary).resolve()), 'facts': str(self.home/'facts'),
                         'state_dir': str(self.home/'state'), 'host': 'synthetic-host', 'background_dream': False}
        self.config.write_text(json.dumps(self.settings))
        self.env = {k: v for k, v in os.environ.items() if not k.startswith(('MEMCUE_', 'CLAUDE_', 'QWEN_'))}
        self.env.update(HOME=str(self.home), GIT_CONFIG_GLOBAL='/dev/null',
                        MEMCUE_FACTS='/do/not/use', MEMCUE_API_URL='https://must-not-call.invalid',
                        MEMCUE_DREAM_BACKGROUND='1', CLAUDE_CONFIG_DIR=str(self.home/'claude'))
        self.guard = self.home/'guard'
        self.guard.mkdir()
        # A leaked model invocation would leave a marker, even on a host with Claude installed.
        for name in ('claude', 'curl', 'codex', 'qwen'):
            p = self.guard/name
            p.write_text('#!/bin/sh\necho unexpected > "'+str(self.home/'unexpected')+'"\nexit 99\n')
            p.chmod(0o700)
        self.env['PATH'] = str(self.guard)+os.pathsep+self.env['PATH']

    def cli(self, *args, cwd=None, ok=True, data=''):
        r = subprocess.run([sys.executable, '-I', str(ADAPTER), '--config', str(self.config), *args],
                           cwd=cwd or self.nested, env=self.env, text=True, input=data, capture_output=True, timeout=70)
        if ok:
            self.assertEqual(r.returncode, 0, r.stderr)
        else:
            self.assertNotEqual(r.returncode, 0, r.stdout)
        return r

    def search(self, *args, cwd=None):
        return json.loads(self.cli('search', '-json', '-min-score', '0', '-n', '100', *args, cwd=cwd).stdout)

    def add(self, content, *args):
        return self.cli('add', *args, '--', content).stdout.strip()

    def dump(self):
        return [json.loads(line) for path in sorted((self.home/'facts').glob('*/*.jsonl'))
                for line in path.read_text().splitlines() if line]

    def pitfall_hook(self, event, session, cwd=None, **fields):
        payload = dict(hook_event_name=event, session_id=session, cwd=str(cwd or self.nested),
                       permission_mode='default', transcript_path=None, **fields)
        if event == 'SessionStart':
            payload.setdefault('source', 'startup')
        return json.loads(self.cli('hook', data=json.dumps(payload)).stdout)['hookSpecificOutput']['additionalContext']

    def test_repository_pitfalls_on_task_and_target_switch(self):
        text = "Qwen fixture failed because its schema was stale; regenerate its whole schema."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        task = self.pitfall_hook('UserPromptSubmit', 'pitfalls-task', prompt='Unrelated task')
        self.assertIn(text, task, 'task hook omitted automatic repository pitfall')
        other = self.home/'other'
        subprocess.run(['git', 'init', '-q', str(other)], env=self.env, check=True)
        subprocess.run(['git', '-C', str(other), 'remote', 'add', 'origin',
                        'https://example.invalid/team/other.git'], env=self.env, check=True)
        switched = self.pitfall_hook('PreToolUse', 'pitfalls-task', tool_name='run_shell_command',
                                     tool_input={'command': 'pwd', 'directory': str(other)})
        self.assertNotIn(text, switched)
        self.assertIn('No active repository pitfalls', switched)
        self.assertIn('supersedes', switched)

    def test_unchanged_pitfall_snapshot_is_sent_once_per_context(self):
        text = "Qwen fixture failed because its cache was stale; clear the whole cache."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        other = self.home/'other'
        subprocess.run(['git', 'init', '-q', str(other)], env=self.env, check=True)
        subprocess.run(['git', '-C', str(other), 'remote', 'add', 'origin',
                        'https://example.invalid/team/other.git'], env=self.env, check=True)
        shell = lambda target: dict(tool_name='run_shell_command', tool_input={'command': 'pwd', 'directory': str(target)})
        self.pitfall_hook('SessionStart', 'dedupe')
        self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='Task'))
        self.assertEqual(self.pitfall_hook('PreToolUse', 'dedupe', **shell(self.nested)), '',
                         'an unchanged snapshot was sent again')
        self.assertEqual(self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='Next'), '')
        switched = self.pitfall_hook('PreToolUse', 'dedupe', **shell(other))
        self.assertIn('No active repository pitfalls', switched)
        self.assertNotIn(text, switched)
        self.assertIn(text, self.pitfall_hook('PreToolUse', 'dedupe', **shell(self.nested)),
                      'returning to the repository must restore its snapshot over the other one')
        self.assertIn(text, self.pitfall_hook('PreToolUse', 'another', **shell(self.nested)),
                      'sessions must not share delivered snapshots')
        self.pitfall_hook('SessionStart', 'dedupe', source='compact')
        self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='After'),
                      'compaction drops the snapshot from context; it must be sent again')

    def test_unreadable_snapshot_state_still_delivers(self):
        text = "Qwen fixture failed because its index was stale; rebuild the whole index."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        (self.home/'state'/'qwen-pitfall-snapshots.json').mkdir(parents=True)
        for turn in ('one', 'two'):
            self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'broken-state', prompt='Task'))

    def test_session_start_delivers_note_and_dream_hint_without_facts(self):
        fact = self.add('Synthetic orchard reviews require the amber label.', '-kind', 'rule', '-source', 'user', '-scope', 'repo')
        (self.home/'state/dream.at').write_text('2000-01-01T00:00:00Z\n')
        note = self.pitfall_hook('SessionStart', 'synthetic-session', source='compact')
        self.assertIn('facts wait for the dream', note)
        self.assertIn('Run every memory command through this launcher', note)
        self.assertNotIn('amber label', note)
        self.assertNotIn('memcue_dream_prepare', note)
        self.assertFalse((self.home/'state/dream.started').exists())
        self.assertIn(fact, [f['id'] for f in self.dump()])

    def test_cross_session_crud_preserves_history_provenance_and_expiry(self):
        self.assertEqual(self.search('synthetic orchard')['facts'], [])
        fact = self.add('Synthetic orchard reviews require the amber label.', '-kind', 'rule', '-scope', 'repo',
                        '-source', 'user', '-expires', '2099-12-31')
        result = self.search('-kind', 'rule,decision', 'synthetic orchard')
        self.assertEqual([x['id'] for x in result['facts']], [fact])
        hit = result['facts'][0]
        self.assertEqual(hit['scope'], 'repo:synthetic.invalid/team/garden')
        self.assertIn('synthetic.invalid/team/garden', hit['origin'])
        self.assertEqual(hit['expires'], '2099-12-31')
        self.assertNotIn('source', hit)
        self.assertNotIn('kind', hit)
        self.cli('correct', fact, 'Synthetic orchard reviews require the violet label.')
        self.assertIn('violet', self.search('synthetic orchard')['facts'][0]['content'])
        self.cli('retire', fact)
        self.assertEqual(self.search('synthetic orchard')['facts'], [])
        history = [f for f in self.dump() if f['id'] == fact]
        self.assertEqual([f['version'] for f in history], [1, 2, 3])
        self.assertEqual([f['status'] for f in history], ['active', 'active', 'retired'])
        for f in history:
            self.assertEqual((f['source'], f['kind'], f['scope'], f['expires']),
                             ('user', 'rule', 'repo:synthetic.invalid/team/garden', '2099-12-31'))
        self.assertFalse((self.home/'unexpected').exists())

    def test_sessions_fallback_reads_qwen_transcripts(self):
        chats = self.home/'.qwen'/'projects'/'-synthetic-garden'/'chats'
        chats.mkdir(parents=True)
        (chats/'s-old.jsonl').write_text(json.dumps({
            'type': 'user', 'provenance': 'real_user', 'sessionId': 's-old', 'uuid': 'u1',
            'cwd': str(self.project), 'timestamp': '2026-10-20T09:00:00Z',
            'message': {'role': 'user', 'parts': [{'text': 'The synthetic marzipan gate uses port 7777.'}]}})+'\n')
        out = self.cli('sessions', '-session', 'current', 'synthetic', 'marzipan').stdout
        self.assertIn('port 7777', out)
        self.assertIn('s-old', out)
        self.assertFalse((self.home/'unexpected').exists())

    def test_dream_prepare_apply_rejects_stale_packet(self):
        fact = self.add('Synthetic orchard reviews require the amber label.', '-kind', 'rule', '-source', 'user', '-scope', 'repo')
        (self.home/'state/dream.at').write_text('2000-01-01T00:00:00Z\n')
        self.cli('dream', '-prepare')
        packet = json.loads((self.home/'state/dream.packet.json').read_text())
        answer = self.home/'answer.json'
        answer.write_text(json.dumps({'packet': packet['id'], 'splits': [], 'actions': [], 'scopes': []}))
        before = self.dump()
        obsolete = self.home/'obsolete-answer.json'
        obsolete.write_text(json.dumps({'packet': packet['id'],
            'splits': [{'id': fact, 'parts': ['Synthetic orchard reviews require labels.', 'The label is amber.'], 'reason': 'fixture'}],
            'actions': [], 'scopes': [], 'expansions': [{'id': fact, 'queries': ['obsolete marker']}]}))
        refused = self.cli('dream', '-apply', str(obsolete), ok=False)
        self.assertIn('unknown field', refused.stderr)
        self.assertEqual(self.dump(), before)
        self.cli('dream', '-apply', str(answer))
        self.add('Synthetic orchard labels remain visible.', '-kind', 'rule', '-source', 'user', '-scope', 'repo')
        self.cli('dream', '-prepare')
        r = self.cli('dream', '-apply', str(answer), ok=False)
        self.assertIn('run -prepare again', r.stderr)
        self.assertFalse((self.home/'unexpected').exists())

    def test_denied_write_is_visible_without_changing_configuration_or_history(self):
        (self.home/'blocked').write_text('unchanged')
        self.settings['facts'] = str(self.home/'blocked/facts')
        self.config.write_text(json.dumps(self.settings))
        r = self.cli('add', '-source', 'user', '--', 'Synthetic blocked preference.', ok=False)
        self.assertIn('memcue command failed', r.stderr)
        self.assertLess(len(r.stderr), 750)
        self.assertEqual((self.home/'blocked').read_text(), 'unchanged')
        self.assertEqual(json.loads(self.config.read_text()), self.settings)
        self.assertFalse((self.home/'.claude').exists())

    def test_installed_copy_runs_without_repository_files(self):
        installed = self.home/'standalone plugin'
        shutil.copytree(ROOT, installed)
        r = subprocess.run([sys.executable, '-I', str(installed/'scripts/memcue.py'), '--config', str(self.config), 'instructions'],
                           cwd=self.nested, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn(str(installed/'skills/memcue/SKILL.md'), r.stdout)


if __name__ == '__main__':
    unittest.main()
