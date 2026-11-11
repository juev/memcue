"""Codex SessionStart delivery and CLI boundary tests (stdlib only)."""
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
        self.tmp = tempfile.TemporaryDirectory(prefix='memcue-codex-')
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.cwd = self.home / 'project' / 'nested'
        self.cwd.mkdir(parents=True)
        self.binary = self.home / 'fake memcue'
        self.binary.write_text('''#!/usr/bin/env python3
import json,os,sys
from pathlib import Path
p=Path(os.environ['MEMCUE_STATE_DIR']);p.mkdir(exist_ok=True)
with (p/'calls').open('a') as f:f.write(json.dumps({'args':sys.argv[1:],'cwd':os.getcwd(),'env':{k:v for k,v in os.environ.items() if k.startswith('MEMCUE_')}})+'\\n')
if sys.argv[1]=='session-card':
 print(json.dumps({'hookSpecificOutput':{'hookEventName':'SessionCard','additionalContext':'memcue: 2 facts wait for the dream. call memcue_dream_prepare and answer its packet with memcue_dream_apply'}}))
elif sys.argv[1]=='search':print(json.dumps({'facts':[],'hints':['No facts; try -in all.']}))
if os.environ.get('MEMCUE_OUTCOME_NONCE'):
 import hashlib
 sys.stderr.write('\\nMEMCUE_OUTCOME_V1 '+json.dumps({'version':1,'nonce':os.environ['MEMCUE_OUTCOME_NONCE'],'status':'success','bytes':0,'sha256':hashlib.sha256(b'').hexdigest(),'error_bytes':0,'error_sha256':hashlib.sha256(b'').hexdigest()})+'\\n')

''')
        self.binary.chmod(0o700)
        self.config = self.home / 'memcue.json'
        self.config.write_text(json.dumps({'binary': str(self.binary), 'facts': str(self.home/'facts'),
                                          'state_dir': str(self.home/'state'), 'host': 'synthetic-host'}))
        self.env = {k:v for k,v in os.environ.items() if not k.startswith(('MEMCUE_', 'CLAUDE_'))}
        self.env.update(HOME=str(self.home), CODEX_HOME=str(self.home/'codex'),
                        MEMCUE_API_URL='https://must-not-call.invalid', MEMCUE_DREAM_BACKGROUND='1',
                        MEMCUE_FACTS='/must/not/use', MEMCUE_SEARCH_LOG='/must/not/log')

    def run_adapter(self, *args, data='', cwd=None):
        return subprocess.run([sys.executable, '-I', str(ADAPTER), '--config', str(self.config), *args],
                              input=data, text=True, capture_output=True, env=self.env,
                              cwd=cwd or self.cwd, timeout=20)

    def payload(self, source='startup'):
        return json.dumps({'hook_event_name':'SessionStart','session_id':'synthetic-session',
                           'cwd':str(self.cwd),'source':source,'model':'synthetic-model',
                           'permission_mode':'default','transcript_path':None})

    def calls(self):
        return [json.loads(x) for x in (self.home/'state'/'calls').read_text().splitlines()]

    def test_delivers_policy_and_adapts_maintenance_for_all_sources(self):
        for source in ('startup','resume','clear','compact','fork'):
            with self.subTest(source=source):
                r = self.run_adapter('hook', data=self.payload(source))
                self.assertEqual(r.returncode, 0, r.stderr)
                obj = json.loads(r.stdout)
                self.assertNotIn('continue', obj)
                h = obj['hookSpecificOutput']
                self.assertEqual(h['hookEventName'], 'SessionStart')
                text = h['additionalContext']
                for term in ('-kind rule,decision', '-in global', '-in shelf:research', 'Search first',
                             'English', 'correct', 'retire', 'source', 'expiry', 'facts wait for the dream',
                             'dream -prepare -all', 'dream -prepare -since DATE', 'no dream yet on this machine'):
                    self.assertIn(term, text)
                for forbidden in ('memcue sessions', 'built-in memory', 'memcue_dream_prepare', 'claude -p'):
                    self.assertNotIn(forbidden, text)
                self.assertIn(str(self.config), text)
                self.assertEqual(self.calls()[-1]['cwd'],str(self.cwd))

    def test_delivered_english_first_policy_preserves_legacy_memory(self):
        # #669 removes translation work; the working model must not recreate it as corrections.
        hook=self.run_adapter('hook',data=self.payload())
        self.assertEqual(hook.returncode,0,hook.stderr)
        note=json.loads(hook.stdout)['hookSpecificOutput']['additionalContext']
        fallback=self.run_adapter('instructions')
        self.assertEqual(fallback.returncode,0,fallback.stderr)
        for text in (note,fallback.stdout,(ROOT/'skills/memcue/SKILL.md').read_text(),
                     (ROOT/'skills/memcue-dream/SKILL.md').read_text()):
            with self.subTest(text=text[:60]):
                self.assertIn('Never correct a fact solely to change its language.',text)
                self.assertIn('Existing fact content remains as stored.',text)

    def test_delivered_policy_names_no_pre_command(self):
        # #712: pre-command is gone, so neither the hook note nor the fallback instructions may send the agent to it.
        hook=self.run_adapter('hook',data=self.payload())
        self.assertEqual(hook.returncode,0,hook.stderr)
        note=json.loads(hook.stdout)['hookSpecificOutput']['additionalContext']
        fallback=self.run_adapter('instructions')
        self.assertEqual(fallback.returncode,0,fallback.stderr)
        for text in (note,fallback.stdout):
            with self.subTest(text=text[:60]):
                self.assertNotIn('pre-command',text)
                self.assertNotIn('-ack',text)
                self.assertNotIn('-in project',text)

    def test_later_shell_calls_use_same_config_without_hook_environment(self):
        r = self.run_adapter('hook',data=self.payload())
        self.assertEqual(r.returncode,0,r.stderr)
        r = self.run_adapter('search','-json','synthetic')
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertEqual(json.loads(r.stdout),{'facts':[],'hints':['No facts; try -in all.']})
        for call in self.calls():
            env = call['env']
            self.assertEqual(env['MEMCUE_FACTS'],str(self.home/'facts'))
            self.assertEqual(env['MEMCUE_STATE_DIR'],str(self.home/'state'))
            self.assertEqual(env['MEMCUE_HOST'],'synthetic-host')
            self.assertEqual(env['MEMCUE_DREAM_BACKGROUND'],'0')
            self.assertNotIn('MEMCUE_API_URL',env)
            self.assertNotIn('MEMCUE_SEARCH_LOG',env)


    def test_invalid_hook_input_is_visible_bounded_and_nonblocking(self):
        for raw in ('not json', 'null', '[]', '{}', self.payload().replace('startup', 'unknown'),
                    self.payload().replace(str(self.cwd), '/not/a/synthetic/directory'), 'x' * 65537):
            with self.subTest(raw=raw[:60]):
                r = self.run_adapter('hook', data=raw)
                self.assertEqual(r.returncode, 0, r.stderr)
                response = json.loads(r.stdout)
                self.assertIn('memcue (Codex)', response['systemMessage'])
                self.assertLess(len(response['systemMessage']), 750)
                self.assertNotIn('continue',response)
                self.assertIn('Other Codex work can continue',r.stderr)

    def test_missing_incompatible_and_failed_commands_remain_visible(self):
        for program in ('#!/usr/bin/env python3\nimport sys\nsys.stderr.write("synthetic denied "*1000)\nsys.exit(1)\n',
                        '#!/usr/bin/env python3\nimport sys\nif sys.argv[1]=="session-card":print("[]")\n',
                        '#!/usr/bin/env python3\nimport sys,json\nif sys.argv[1]=="session-card":print(json.dumps({"hookSpecificOutput": []}))\n'):
            self.binary.write_text(program)
            r = self.run_adapter('hook',data=self.payload())
            self.assertEqual(r.returncode,0,r.stderr)
            self.assertIn('systemMessage',json.loads(r.stdout))
            self.assertLess(len(r.stderr),750)
        self.binary.unlink()
        r = self.run_adapter('hook',data=self.payload())
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn('missing',json.loads(r.stdout)['systemMessage'])
        r = self.run_adapter('search','synthetic')
        self.assertNotEqual(r.returncode,0)
        self.assertIn('missing',r.stderr)

    def test_config_without_paths_uses_the_machine_binary_and_shared_base(self):
        # #718: without binary, facts and state_dir the launcher runs ~/.local/bin/memcue on memcue's own default
        # base, the one every agent of the machine shares.
        machine = self.home/'.local'/'bin'/'memcue'
        machine.parent.mkdir(parents=True)
        log = self.home/'machine-calls'
        machine.write_text('#!/usr/bin/env python3\nimport json,os,sys\n'
                           f'open({str(log)!r},"a").write(json.dumps({{"args":sys.argv[1:],"env":{{k:v for k,v in os.environ.items() if k.startswith("MEMCUE_")}}}})+"\\n")\n'
                           'if sys.argv[1]=="session-card":print("")\n')
        machine.chmod(0o700)
        self.config.write_text(json.dumps({'host': 'synthetic-host'}))
        self.env.update(XDG_STATE_HOME=str(self.home/'xdg'), CLAUDE_CONFIG_DIR=str(self.home/'claude'))
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

    def test_invalid_config_never_selects_default_base(self):
        valid = json.loads(self.config.read_text())
        for change in ({'facts':''},{'facts':'relative'}, {'state_dir':'relative'}, {'binary':'memcue'},
                       {'host':''}, {'background_dream': True}, {'unknown':True}):
            self.config.write_text(json.dumps(valid | change))
            r = self.run_adapter('hook',data=self.payload())
            self.assertEqual(r.returncode,0,r.stderr)
            self.assertIn('systemMessage',json.loads(r.stdout))
            self.assertFalse((self.home/'state').exists())
        self.config.unlink()
        r = self.run_adapter('hook',data=self.payload())
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn('systemMessage',json.loads(r.stdout))
        self.assertFalse((self.home/'.claude').exists())

    def test_unsupported_runner_and_history_commands_cannot_start(self):
        for args in (('dream','-run'),('sessions','synthetic'),('session-start',)):
            r=self.run_adapter(*args)
            self.assertNotEqual(r.returncode,0)
            self.assertFalse((self.home/'state').exists())

    def test_dream_allowlist_passes_only_documented_packet_commands(self):
        for args in (('-prepare',),('-prepare','-all'),('-prepare','-since','2000-01-01'),
                     ('-prepare','-since','2000-01-01T00:00:00Z'),('-list',),('-apply','/synthetic/answer.json'),
                     ('--','-prepare','-all')):
            with self.subTest(args=args):
                r=self.run_adapter('dream',*args)
                self.assertEqual(r.returncode,0,r.stderr)
                # argparse consumes a leading option terminator before the remainder.
                forwarded=args[1:] if args[0]=='--' else args
                self.assertEqual(self.calls()[-1]['args'],['dream',*forwarded])
                self.assertEqual(self.calls()[-1]['env']['MEMCUE_DREAM_BACKGROUND'],'0')
                self.assertNotIn('MEMCUE_API_URL',self.calls()[-1]['env'])

    def test_dream_rejects_extra_duplicate_and_malformed_flags_before_any_child(self):
        for args in ((),('-all',),('-since','2000-01-01'),('-prepare','-prepare'),
                     ('-prepare','-all','-all'),('-prepare','-all','-since','2000-01-01'),
                     ('-prepare','-since','2000-01-01','-since','2001-01-01'),
                     ('-prepare','-since'),('-prepare','-since',''),('-prepare','-since','-run'),
                     ('-prepare','-since','2000-01-01','extra'),('-prepare','-all','extra'),
                     ('-prepare','-all=true'),('-prepare','-since=2000-01-01'),('-all','-prepare'),
                     ('-prepare','-run'),('-prepare','-background'),('-prepare','-j','2'),
                     ('-run','-all'),('-run','-background'),('-list','-all'),('-list','-prepare'),
                     ('-apply',),('-apply','/synthetic/answer.json','-all'),('--','-prepare','-run')):
            with self.subTest(args=args):
                r=self.run_adapter('dream',*args)
                self.assertNotEqual(r.returncode,0,r.stdout)
                self.assertIn('dream accepts only',r.stderr)
                self.assertFalse((self.home/'state').exists())

    def test_empty_maintenance_response_still_delivers_policy(self):
        self.binary.write_text('#!/usr/bin/env python3\n')
        r=self.run_adapter('hook',data=self.payload())
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn('-kind rule,decision',json.loads(r.stdout)['hookSpecificOutput']['additionalContext'])
        self.assertNotIn('systemMessage',json.loads(r.stdout))


    def test_fallback_is_rendered_without_overwriting_existing_instructions(self):
        original = b'# Existing instructions\nKeep this exactly.\n'
        agents = self.cwd / 'AGENTS.md'
        agents.write_bytes(original)
        r = self.run_adapter('instructions')
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertEqual(agents.read_bytes(),original)
        for term in ('-kind rule,decision','-in global','-in shelf:research','English',str(self.config),
                     'Search first','memcue-dream','expiry','source','kind','scope',
                     'dream -prepare -all','dream -prepare -since DATE','no dream yet on this machine'):
            self.assertIn(term,r.stdout)
        self.assertNotIn('@MEMCUE@',r.stdout)
        self.assertFalse((self.home/'state').exists())

    def test_plugin_delivers_lifecycle_hooks_and_cli_skills(self):
        manifest=json.loads((ROOT/'.codex-plugin/plugin.json').read_text())
        self.assertEqual(manifest['name'],'memcue')
        self.assertEqual(manifest['skills'],'./skills/')
        hooks=json.loads((ROOT/'hooks/hooks.json').read_text())['hooks']
        self.assertEqual(set(hooks),{'SessionStart', 'UserPromptSubmit', 'PreToolUse'})
        handler=hooks['SessionStart'][0]['hooks'][0]
        self.assertIn('PLUGIN_ROOT',handler['command'])
        self.assertIn('memcue.py',handler['command'])
        self.assertIn('hook',handler['command'])
        self.assertLessEqual(handler['timeout'],30)
        for name in ('memcue','memcue-dream'):
            skill=(ROOT/'skills'/name/'SKILL.md').read_text()
            self.assertIn('configured launcher',skill)
            self.assertNotIn('memcue sessions',skill)
            self.assertNotIn('claude -p',skill)


    def test_timeout_returns_a_visible_error_without_leaving_the_child_running(self):
        spec = importlib.util.spec_from_file_location('codex_memcue_adapter',ADAPTER)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        self.binary.write_text('#!/usr/bin/env python3\nimport time\ntime.sleep(1)\n')
        config = json.loads(self.config.read_text())
        with self.assertRaisesRegex(module.AdapterError,'timed out.*persisted state'):
            module.call(config,['search','synthetic'],self.cwd,timeout=0.03)

    def test_rendered_launcher_handles_shell_metacharacters_in_paths(self):
        special = self.home / "config ' $(touch should-not-exist).json"
        self.config.rename(special)
        self.config = special
        r = self.run_adapter('instructions')
        self.assertEqual(r.returncode,0,r.stderr)
        command = r.stdout.split('configured launcher: `',1)[1].split('`.',1)[0]
        result = subprocess.run(['/bin/sh','-c',command+' search -json synthetic'],cwd=self.cwd,env=self.env,
                                text=True,capture_output=True,timeout=20)
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertEqual(json.loads(result.stdout)['facts'],[])
        self.assertFalse((self.cwd/'should-not-exist').exists())


    def test_hook_version_timeout_leaves_room_for_its_diagnostic(self):
        # The outer Codex handler has 30 seconds for both children and JSON delivery.
        self.binary.write_text('#!/usr/bin/env python3\nimport sys,time\nif sys.argv[1]=="version":time.sleep(6)\n')
        r=self.run_adapter('hook',data=self.payload())
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn('timed out',json.loads(r.stdout).get('systemMessage',''))


if __name__ == '__main__':
    unittest.main()
