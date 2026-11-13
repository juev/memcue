"""Qwen Code SessionStart delivery and CLI boundary tests (stdlib only)."""
import json
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
ADAPTER = ROOT / 'scripts' / 'memcue.py'


class AdapterTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix='memcue-qwen-')
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.cwd = self.home / 'project' / 'nested'
        self.cwd.mkdir(parents=True)
        self.binary = self.home / 'fake memcue'
        self.binary.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
p=Path(os.environ['MEMCUE_STATE_DIR']);p.mkdir(exist_ok=True)
inp={}
try:
    inp=json.loads(sys.stdin.read() or '{}')
except Exception:
    pass
with (p/'calls').open('a') as f:f.write(json.dumps({'args':sys.argv[1:],'cwd':os.getcwd(),'env':{k:v for k,v in os.environ.items() if k.startswith('MEMCUE_')}})+'\\n')
if sys.argv[1]=='session-start':
    print(json.dumps({'hookSpecificOutput':{'hookEventName':'SessionStart','additionalContext':'memcue note: search -kind rule,decision; facts wait for the dream; run the memcue-dream skill'}}))
elif sys.argv[1]=='session-card':
    print(json.dumps({'hookSpecificOutput':{'hookEventName':inp.get('hook_event_name','SessionCard'),'additionalContext':'Repository pitfalls for synthetic/repo: snapshot.\\n\\nSynthetic pitfall.'}}))
elif sys.argv[1]=='search':print(json.dumps({'facts':[],'hints':['No facts; try -in all.']}))
if os.environ.get('MEMCUE_OUTCOME_NONCE'):
    import hashlib
    sys.stderr.write('\\nMEMCUE_OUTCOME_V1 '+json.dumps({'version':1,'nonce':os.environ['MEMCUE_OUTCOME_NONCE'],'status':'success','bytes':0,'sha256':hashlib.sha256(b'').hexdigest(),'error_bytes':0,'error_sha256':hashlib.sha256(b'').hexdigest()})+'\\n')
''')
        self.binary.chmod(0o700)
        self.config = self.home / 'memcue.json'
        self.config.write_text(json.dumps({'binary': str(self.binary), 'facts': str(self.home/'facts'),
                                          'state_dir': str(self.home/'state'), 'host': 'synthetic-host'}))
        self.env = {k:v for k,v in os.environ.items() if not k.startswith(('MEMCUE_', 'CLAUDE_', 'QWEN_'))}
        self.env.update(HOME=str(self.home), MEMCUE_API_URL='https://must-not-call.invalid',
                        MEMCUE_DREAM_BACKGROUND='1', MEMCUE_FACTS='/must/not/use', MEMCUE_SEARCH_LOG='/must/not/log')

    def run_adapter(self, *args, data='', cwd=None):
        return subprocess.run([sys.executable, '-I', str(ADAPTER), '--config', str(self.config), *args],
                              input=data, text=True, capture_output=True, env=self.env,
                              cwd=cwd or self.cwd, timeout=20)

    def payload(self, source='startup', event='SessionStart', **fields):
        base = {'hook_event_name': event, 'session_id': 'synthetic-session', 'cwd': str(self.cwd),
                'permission_mode': 'default', 'transcript_path': None}
        if event == 'SessionStart':
            base['source'] = source
        base.update(fields)
        return json.dumps(base)

    def calls(self):
        return [json.loads(x) for x in (self.home/'state'/'calls').read_text().splitlines()]

    def test_session_start_delivers_launcher_preamble_and_binary_note(self):
        for source in ('startup', 'resume', 'clear', 'compact'):
            with self.subTest(source=source):
                r = self.run_adapter('hook', data=self.payload(source))
                self.assertEqual(r.returncode, 0, r.stderr)
                obj = json.loads(r.stdout)
                self.assertNotIn('continue', obj)
                h = obj['hookSpecificOutput']
                self.assertEqual(h['hookEventName'], 'SessionStart')
                text = h['additionalContext']
                self.assertIn(str(self.config), text)
                self.assertIn('Run every memory command through this launcher', text)
                self.assertIn('memcue note: search -kind rule,decision', text)
                self.assertIn('facts wait for the dream', text)
                self.assertEqual(self.calls()[-1]['cwd'], str(self.cwd.resolve()))
                self.assertEqual(self.calls()[-1]['env']['MEMCUE_AGENT'], 'qwen')

    def test_session_start_rejects_unsupported_source(self):
        r = self.run_adapter('hook', data=self.payload('fork'))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('memcue (Qwen)', json.loads(r.stdout)['systemMessage'])

    def test_pitfall_snapshot_dedup_per_session_and_context(self):
        first = self.run_adapter('hook', data=self.payload(event='UserPromptSubmit', prompt='Task'))
        self.assertEqual(first.returncode, 0, first.stderr)
        h = json.loads(first.stdout)['hookSpecificOutput']
        self.assertEqual(h['hookEventName'], 'UserPromptSubmit')
        self.assertIn('Synthetic pitfall.', h['additionalContext'])
        again = self.run_adapter('hook', data=self.payload(event='UserPromptSubmit', prompt='Next'))
        self.assertEqual(json.loads(again.stdout)['hookSpecificOutput']['additionalContext'], '')
        shell = self.run_adapter('hook', data=self.payload(event='PreToolUse', tool_name='run_shell_command',
                                                           tool_input={'command': 'pwd'}))
        self.assertEqual(json.loads(shell.stdout)['hookSpecificOutput']['additionalContext'], '')
        other = self.run_adapter('hook', data=self.payload(event='UserPromptSubmit', session_id='another', prompt='Task'))
        self.assertIn('Synthetic pitfall.', json.loads(other.stdout)['hookSpecificOutput']['additionalContext'])
        self.run_adapter('hook', data=self.payload('compact'))
        after = self.run_adapter('hook', data=self.payload(event='UserPromptSubmit', prompt='After'))
        self.assertIn('Synthetic pitfall.', json.loads(after.stdout)['hookSpecificOutput']['additionalContext'],
                      'compaction drops the snapshot from context; it must be sent again')

    def test_pre_tool_use_directory_override(self):
        other = self.home / 'other'
        other.mkdir()
        r = self.run_adapter('hook', data=self.payload(event='PreToolUse', tool_name='run_shell_command',
                                                       tool_input={'command': 'pwd', 'directory': str(other)}))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.calls()[-1]['cwd'], str(other.resolve()))

    def test_pre_tool_use_rejects_other_tools(self):
        r = self.run_adapter('hook', data=self.payload(event='PreToolUse', tool_name='read_file', tool_input={}))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('memcue (Qwen)', json.loads(r.stdout)['systemMessage'])

    def test_later_shell_calls_use_same_config_without_hook_environment(self):
        r = self.run_adapter('hook', data=self.payload())
        self.assertEqual(r.returncode, 0, r.stderr)
        r = self.run_adapter('search', '-json', 'synthetic')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(json.loads(r.stdout), {'facts': [], 'hints': ['No facts; try -in all.']})
        for call in self.calls():
            env = call['env']
            self.assertEqual(env['MEMCUE_FACTS'], str(self.home/'facts'))
            self.assertEqual(env['MEMCUE_STATE_DIR'], str(self.home/'state'))
            self.assertEqual(env['MEMCUE_HOST'], 'synthetic-host')
            self.assertEqual(env['MEMCUE_DREAM_BACKGROUND'], '0')
            self.assertEqual(env['MEMCUE_AGENT'], 'qwen')
            self.assertNotIn('MEMCUE_API_URL', env)
            self.assertNotIn('MEMCUE_SEARCH_LOG', env)

    def test_invalid_hook_input_is_visible_bounded_and_nonblocking(self):
        for raw in ('not json', 'null', '[]', '{}', self.payload('unknown'),
                    self.payload().replace(str(self.cwd), '/not/a/synthetic/directory'), 'x' * 65537):
            with self.subTest(raw=raw[:60]):
                r = self.run_adapter('hook', data=raw)
                self.assertEqual(r.returncode, 0, r.stderr)
                response = json.loads(r.stdout)
                self.assertIn('memcue (Qwen)', response['systemMessage'])
                self.assertLess(len(response['systemMessage']), 750)
                self.assertNotIn('continue', response)
                self.assertIn('Other Qwen work can continue', r.stderr)

    def test_invalid_permission_mode_is_rejected(self):
        r = self.run_adapter('hook', data=self.payload(permission_mode='bypassPermissions'))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('permission_mode', json.loads(r.stdout)['systemMessage'])

    def test_missing_and_failed_commands_remain_visible(self):
        self.binary.write_text('#!/usr/bin/env python3\nimport sys\nsys.stderr.write("synthetic denied "*1000)\nsys.exit(1)\n')
        r = self.run_adapter('hook', data=self.payload())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('systemMessage', json.loads(r.stdout))
        self.assertLess(len(r.stderr), 750)
        self.binary.unlink()
        r = self.run_adapter('hook', data=self.payload())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('missing', json.loads(r.stdout)['systemMessage'])
        r = self.run_adapter('search', 'synthetic')
        self.assertNotEqual(r.returncode, 0)
        self.assertIn('missing', r.stderr)

    def test_config_without_paths_uses_the_machine_binary_and_shared_base(self):
        machine = self.home/'.local'/'bin'/'memcue'
        machine.parent.mkdir(parents=True)
        log = self.home/'machine-calls'
        machine.write_text('#!/usr/bin/env python3\nimport json,os,sys\n'
                           f'open({str(log)!r},"a").write(json.dumps({{"args":sys.argv[1:],"env":{{k:v for k,v in os.environ.items() if k.startswith("MEMCUE_")}}}})+"\\n")\n'
                           'if sys.argv[1]=="session-start":print(json.dumps({"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"note"}}))\n'
                           'elif sys.argv[1]=="session-card":print("")\n')
        machine.chmod(0o700)
        self.config.write_text(json.dumps({'host': 'synthetic-host'}))
        self.env.update(XDG_STATE_HOME=str(self.home/'xdg'))
        r = self.run_adapter('hook', data=self.payload())
        self.assertEqual(r.returncode, 0, r.stderr)
        r = self.run_adapter('search', '-json', 'synthetic')
        self.assertEqual(r.returncode, 0, r.stderr)
        calls = [json.loads(x) for x in log.read_text().splitlines()]
        self.assertTrue(calls)
        for call in calls:
            self.assertNotIn('MEMCUE_FACTS', call['env'], 'an inherited MEMCUE_FACTS must not pick the base')
            self.assertNotIn('MEMCUE_STATE_DIR', call['env'])
            self.assertEqual(call['env']['MEMCUE_HOST'], 'synthetic-host')
            self.assertEqual(call['env']['MEMCUE_AGENT'], 'qwen')

    def test_invalid_config_never_selects_default_base(self):
        valid = json.loads(self.config.read_text())
        for change in ({'facts': ''}, {'facts': 'relative'}, {'state_dir': 'relative'}, {'binary': 'memcue'},
                       {'host': ''}, {'background_dream': True}, {'unknown': True}):
            self.config.write_text(json.dumps(valid | change))
            r = self.run_adapter('hook', data=self.payload())
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertIn('systemMessage', json.loads(r.stdout))
            self.assertFalse((self.home/'state').exists())
        self.config.unlink()
        r = self.run_adapter('hook', data=self.payload())
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('systemMessage', json.loads(r.stdout))

    def test_unsupported_runner_commands_cannot_start(self):
        for args in (('dream', '-run'), ('session-start',), ('hook-extract',)):
            r = self.run_adapter(*args)
            self.assertNotEqual(r.returncode, 0)
            self.assertFalse((self.home/'state').exists())

    def test_sessions_command_is_allowed(self):
        r = self.run_adapter('sessions', 'synthetic')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.calls()[-1]['args'], ['sessions', 'synthetic'])

    def test_dream_allowlist_passes_only_documented_packet_commands(self):
        for args in (('-prepare',), ('-prepare', '-all'), ('-prepare', '-since', '2000-01-01'),
                     ('-prepare', '-since', '2000-01-01T00:00:00Z'), ('-list',), ('-apply', '/synthetic/answer.json'),
                     ('--', '-prepare', '-all')):
            with self.subTest(args=args):
                r = self.run_adapter('dream', *args)
                self.assertEqual(r.returncode, 0, r.stderr)
                forwarded = args[1:] if args[0] == '--' else args
                self.assertEqual(self.calls()[-1]['args'], ['dream', *forwarded])
                self.assertEqual(self.calls()[-1]['env']['MEMCUE_DREAM_BACKGROUND'], '0')
                self.assertNotIn('MEMCUE_API_URL', self.calls()[-1]['env'])

    def test_dream_rejects_extra_duplicate_and_malformed_flags_before_any_child(self):
        for args in ((), ('-all',), ('-since', '2000-01-01'), ('-prepare', '-prepare'),
                     ('-prepare', '-all', '-all'), ('-prepare', '-all', '-since', '2000-01-01'),
                     ('-prepare', '-since'), ('-prepare', '-since', ''), ('-prepare', '-since', '-run'),
                     ('-prepare', '-run'), ('-run', '-all'), ('-list', '-all'),
                     ('-apply',), ('-apply', '/synthetic/answer.json', '-all'), ('--', '-prepare', '-run')):
            with self.subTest(args=args):
                r = self.run_adapter('dream', *args)
                self.assertNotEqual(r.returncode, 0, r.stdout)
                self.assertIn('dream accepts only', r.stderr)
                self.assertFalse((self.home/'state').exists())

    def test_fallback_is_rendered_without_overwriting_existing_instructions(self):
        original = b'# Existing instructions\nKeep this exactly.\n'
        qwen_md = self.cwd / 'QWEN.md'
        qwen_md.write_bytes(original)
        r = self.run_adapter('instructions')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(qwen_md.read_bytes(), original)
        for term in ('-kind rule,decision', '-in global', '-in shelf:research', 'English', str(self.config),
                     'Search first', 'memcue-dream', 'sessions', 'dream -prepare -all', 'no dream yet on this machine'):
            self.assertIn(term, r.stdout)
        self.assertNotIn('@MEMCUE@', r.stdout)
        self.assertFalse((self.home/'state').exists())

    def test_plugin_delivers_lifecycle_hooks_and_cli_skills(self):
        manifest = json.loads((ROOT/'qwen-extension.json').read_text())
        self.assertEqual(manifest['name'], 'memcue-qwen')
        self.assertEqual(manifest['skills'], 'skills')
        hooks = json.loads((ROOT/'hooks/hooks.json').read_text())['hooks']
        self.assertEqual(set(hooks), {'SessionStart', 'UserPromptSubmit', 'PreToolUse'})
        for event in ('SessionStart', 'UserPromptSubmit', 'PreToolUse'):
            handler = hooks[event][0]['hooks'][0]
            self.assertIn('CLAUDE_PLUGIN_ROOT', handler['command'])
            self.assertIn('memcue.py', handler['command'])
            self.assertIn('hook', handler['command'])
            self.assertLessEqual(handler['timeout'], 30)
        self.assertEqual(hooks['PreToolUse'][0]['matcher'], '^run_shell_command$')
        for name in ('memcue', 'memcue-dream'):
            skill = (ROOT/'skills'/name/'SKILL.md').read_text()
            self.assertIn('configured launcher', skill)
            self.assertNotIn('claude -p', skill)
        self.assertIn('sessions', (ROOT/'skills/memcue/SKILL.md').read_text())

    def test_timeout_returns_a_visible_error_without_leaving_the_child_running(self):
        spec = importlib.util.spec_from_file_location('qwen_memcue_adapter', ADAPTER)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.binary.write_text('#!/usr/bin/env python3\nimport time\ntime.sleep(1)\n')
        config = json.loads(self.config.read_text())
        with self.assertRaisesRegex(module.AdapterError, 'timed out.*persisted state'):
            module.call(config, ['search', 'synthetic'], self.cwd, timeout=0.03)

    def test_rendered_launcher_handles_shell_metacharacters_in_paths(self):
        special = self.home / "config ' $(touch should-not-exist).json"
        self.config.rename(special)
        self.config = special
        r = self.run_adapter('instructions')
        self.assertEqual(r.returncode, 0, r.stderr)
        command = r.stdout.split('configured launcher: `', 1)[1].split('`.', 1)[0]
        result = subprocess.run(['/bin/sh', '-c', command + ' search -json synthetic'], cwd=self.cwd, env=self.env,
                                text=True, capture_output=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(json.loads(result.stdout)['facts'], [])
        self.assertFalse((self.cwd/'should-not-exist').exists())


if __name__ == '__main__':
    unittest.main()
