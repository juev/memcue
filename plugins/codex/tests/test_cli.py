"""Real memcue CLI through the Codex launcher, using synthetic facts only.

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
REPO = ROOT.parents[1]
ADAPTER = ROOT / 'scripts/memcue.py'


class RealCLI(unittest.TestCase):
    def setUp(self):
        binary = os.environ.get('MEMCUE_BIN')
        self.assertTrue(binary and Path(binary).is_file(), 'build bin/memcue and set MEMCUE_BIN; this suite must execute')
        self.tmp = tempfile.TemporaryDirectory(prefix='codex-synthetic-')
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)
        self.project = self.home/'project'
        (self.project/'.git').mkdir(parents=True)
        (self.project/'.git/config').write_text('[remote "origin"]\nurl = https://synthetic.invalid/team/garden.git\n')
        self.nested = self.project/'a/b'
        self.nested.mkdir(parents=True)
        self.config = self.home/'memcue.json'
        self.settings={'binary':str(Path(binary).resolve()),'facts':str(self.home/'facts'),
                       'state_dir':str(self.home/'state'),'host':'synthetic-host','background_dream':False}
        self.config.write_text(json.dumps(self.settings))
        self.env = {k:v for k,v in os.environ.items() if not k.startswith(('MEMCUE_','CLAUDE_'))}
        self.env.update(HOME=str(self.home),CODEX_HOME=str(self.home/'codex'),GIT_CONFIG_GLOBAL='/dev/null',
                        MEMCUE_FACTS='/do/not/use',MEMCUE_API_URL='https://must-not-call.invalid',
                        MEMCUE_DREAM_BACKGROUND='1',CLAUDE_CONFIG_DIR=str(self.home/'claude'))
        self.guard = self.home/'guard'
        self.guard.mkdir()
        # A leaked model invocation would leave a marker, even on a host with Claude installed.
        for name in ('claude','curl','codex'):
            p=self.guard/name
            p.write_text('#!/bin/sh\necho unexpected > "'+str(self.home/'unexpected')+'"\nexit 99\n')
            p.chmod(0o700)
        self.env['PATH']=str(self.guard)+os.pathsep+self.env['PATH']

    def cli(self, *args, cwd=None, ok=True, data=''):
        r=subprocess.run([sys.executable,'-I',str(ADAPTER),'--config',str(self.config),*args],
                         cwd=cwd or self.nested,env=self.env,text=True,input=data,capture_output=True,timeout=70)
        if ok:
            self.assertEqual(r.returncode,0,r.stderr)
        else:
            self.assertNotEqual(r.returncode,0,r.stdout)
        return r

    def search(self, *args, cwd=None):
        return json.loads(self.cli('search','-json','-min-score','0','-n','100',*args,cwd=cwd).stdout)

    def add(self, content, *args):
        return self.cli('add',*args,'--',content).stdout.strip()

    def dump(self):
        return [json.loads(line) for path in sorted((self.home/'facts').glob('*/*.jsonl'))
                for line in path.read_text().splitlines() if line]

    def test_repository_pitfalls_on_task_and_target_switch(self):
        text = "Codex fixture failed because its schema was stale; regenerate its whole schema."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        def hook(event, cwd, **fields):
            payload = dict(hook_event_name=event, session_id='pitfalls-task', cwd=str(cwd),
                           model='synthetic-model', permission_mode='default', transcript_path=None, **fields)
            result = self.cli('hook', data=json.dumps(payload))
            return json.loads(result.stdout)['hookSpecificOutput']
        for source in ('startup', 'compact'):
            self.assertIn(text, hook('SessionStart', self.nested, source=source)['additionalContext'])
        task = hook('UserPromptSubmit', self.nested, prompt='Unrelated task', turn_id='one')
        self.assertEqual(task['hookEventName'], 'UserPromptSubmit')
        self.assertIn(text, task['additionalContext'], 'task hook omitted automatic repository pitfall')
        other = self.home/'other'
        subprocess.run(['git', 'init', '-q', str(other)], env=self.env, check=True)
        subprocess.run(['git', '-C', str(other), 'remote', 'add', 'origin',
                        'https://example.invalid/team/other.git'], env=self.env, check=True)
        switched = hook('PreToolUse', self.nested, turn_id='one', tool_name='Bash',
                        tool_input={'command':'pwd', 'workdir':str(other)})
        self.assertEqual(switched['hookEventName'], 'PreToolUse')
        self.assertNotIn(text, switched['additionalContext'])
        self.assertIn('No active repository pitfalls', switched['additionalContext'])
        self.assertIn('supersedes', switched['additionalContext'])

    def pitfall_hook(self, event, session, cwd=None, **fields):
        payload = dict(hook_event_name=event, session_id=session, cwd=str(cwd or self.nested),
                       model='synthetic-model', permission_mode='default', transcript_path=None, **fields)
        return json.loads(self.cli('hook', data=json.dumps(payload)).stdout)['hookSpecificOutput']['additionalContext']

    def test_unchanged_pitfall_snapshot_is_sent_once_per_context(self):
        # #705: PreToolUse runs before every shell command; one session repeated one snapshot seven times.
        text = "Codex fixture failed because its cache was stale; clear the whole cache."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        other = self.home/'other'
        subprocess.run(['git', 'init', '-q', str(other)], env=self.env, check=True)
        subprocess.run(['git', '-C', str(other), 'remote', 'add', 'origin',
                        'https://example.invalid/team/other.git'], env=self.env, check=True)
        bash = lambda target: dict(tool_name='Bash', tool_input={'command': 'pwd', 'workdir': str(target)})
        self.pitfall_hook('SessionStart', 'dedupe', source='startup')
        self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='Task', turn_id='one'))
        self.assertEqual(self.pitfall_hook('PreToolUse', 'dedupe', turn_id='one', **bash(self.nested)), '',
                         'an unchanged snapshot was sent again')
        self.assertEqual(self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='Next', turn_id='two'), '')
        switched = self.pitfall_hook('PreToolUse', 'dedupe', turn_id='two', **bash(other))
        self.assertIn('No active repository pitfalls', switched)
        self.assertNotIn(text, switched)
        self.assertIn(text, self.pitfall_hook('PreToolUse', 'dedupe', turn_id='two', **bash(self.nested)),
                      'returning to the repository must restore its snapshot over the other one')
        self.assertIn(text, self.pitfall_hook('PreToolUse', 'another', turn_id='one', **bash(self.nested)),
                      'sessions must not share delivered snapshots')
        self.pitfall_hook('SessionStart', 'dedupe', source='compact')
        self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'dedupe', prompt='After', turn_id='three'),
                      'compaction drops the snapshot from context; it must be sent again')

    def test_unreadable_snapshot_state_still_delivers(self):
        text = "Codex fixture failed because its index was stale; rebuild the whole index."
        self.add(text, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        (self.home/'state'/'codex-pitfall-snapshots.json').mkdir(parents=True)
        for turn in ('one', 'two'):
            self.assertIn(text, self.pitfall_hook('UserPromptSubmit', 'broken-state', prompt='Task', turn_id=turn))

    def test_pitfall_transport_has_no_preview_threshold(self):
        content = ("Codex full condition and workaround: " + "schema repair detail. " * 1200).strip()
        self.add(content, '-kind', 'pitfall', '-scope', 'repo', '-source', 'tool')
        payload = dict(hook_event_name='SessionStart', source='startup', session_id='large-pitfall',
                       cwd=str(self.nested), model='synthetic-model', permission_mode='default', transcript_path=None)
        response = json.loads(self.cli('hook', data=json.dumps(payload)).stdout)
        self.assertIn(content, response['hookSpecificOutput']['additionalContext'])
        hooks = json.loads((ROOT/'hooks/hooks.json').read_text())['hooks']
        for event in ('SessionStart', 'UserPromptSubmit', 'PreToolUse'):
            self.assertEqual(hooks[event][0]['hooks'][0]['additionalContextLimit'], 0,
                             'host would spill complete repository pitfalls to a preview')

    def test_relative_target_resolved_once(self):
        target = self.nested/'target'
        nested = target/'target'
        for path, name in ((target,'intended'),(nested,'unintended')):
            subprocess.run(['git','init','-q',str(path)],env=self.env,check=True)
            subprocess.run(['git','-C',str(path),'remote','add','origin','https://example.invalid/'+name+'.git'],env=self.env,check=True)
            self.cli('add','-cwd',str(path),'-kind','pitfall','-scope','repo','--',name+' failure; repair fixture.')
        for field in ('workdir','cwd'):
            for value in ('target',str(target)):
                # One session per case: an unchanged snapshot is not sent twice in a session (#705).
                payload=dict(hook_event_name='PreToolUse',session_id='relative-'+field+'-'+str(value),cwd=str(self.nested),model='synthetic',permission_mode='default',transcript_path=None,tool_name='Bash',tool_input={'command':'pwd',field:value})
                text=json.loads(self.cli('hook',data=json.dumps(payload)).stdout)['hookSpecificOutput']['additionalContext']
                self.assertIn('intended failure;',text)
                self.assertNotIn('unintended failure;',text)

    def test_pre_command_uses_configured_base_and_actual_cwd(self):
        fact=self.add('Synthetic orchard build requires amber mode.', '-kind','pitfall','-source','tool','-scope','repo')
        args=('pre-command','-cwd',str(self.nested),'-session','synthetic-task','-action','build','synthetic orchard build')
        first=json.loads(self.cli(*args).stdout)
        self.assertIn(fact,[f['id'] for f in first['facts']])
        replay=json.loads(self.cli(*args).stdout)
        self.assertEqual(replay['facts'],first['facts'])
        self.assertTrue(replay['blocked'])
        ready=json.loads(self.cli(*args,'-ack',replay['receipt']).stdout)
        self.assertFalse(ready['blocked'])
        self.assertEqual(ready['facts'],[])
        self.assertEqual(json.loads(self.cli(*args).stdout)['facts'],[])

    def test_pre_command_transport_timeout_replays_before_acknowledgment(self):
        fact=self.add('Synthetic orchard build requires amber mode only for synthetic fixtures.',
                      '-kind','pitfall','-source','tool','-scope','repo')
        wrapper=self.home/'delayed.py'
        marker=self.home/'delayed-once'
        wrapper.write_text('#!'+sys.executable+'\nimport pathlib,subprocess,sys,time\n'
                           +'p=subprocess.run(['+json.dumps(self.settings['binary'])+']+sys.argv[1:])\n'
                           +'marker=pathlib.Path('+json.dumps(str(marker))+')\n'
                           +"if p.returncode==0 and sys.argv[1]=='pre-command' and not marker.exists():\n"
                           +" marker.write_text('synthetic'); time.sleep(1)\nsys.exit(p.returncode)\n")
        wrapper.chmod(0o700)
        self.settings['binary']=str(wrapper)
        self.config.write_text(json.dumps(self.settings))
        runner=self.home/'short-adapter-timeout.py'
        runner.write_text('import importlib.util\n'
                          +'spec=importlib.util.spec_from_file_location("adapter",'+json.dumps(str(ADAPTER))+')\n'
                          +'module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)\n'
                          +'module.TIMEOUT=0.3; import sys; sys.exit(module.main())\n')
        args=('pre-command','-cwd',str(self.nested),'-session','transport-task','-action','build',
              'synthetic orchard build')
        lost=subprocess.run([sys.executable,'-I',str(runner),'--config',str(self.config),*args],
                            cwd=self.nested,env=self.env,text=True,capture_output=True,timeout=5)
        self.assertNotEqual(lost.returncode,0)
        self.assertIn('timed out',lost.stderr)
        self.assertEqual(lost.stdout,'')
        self.assertTrue(marker.exists(),'timeout must follow successful CLI commit')
        replay=json.loads(self.cli(*args).stdout)
        self.assertIn(fact,[f['id'] for f in replay['facts']], 'CLI commit must not imply adapter delivery')
        self.assertTrue(replay['blocked'])
        ready=json.loads(self.cli(*args,'-ack',replay['receipt']).stdout)
        self.assertFalse(ready['blocked'])
        self.assertEqual(ready['facts'],[])

    def test_cross_session_crud_preserves_history_provenance_and_expiry(self):
        self.assertEqual(self.search('synthetic orchard')['facts'],[])
        fact=self.add('Synthetic orchard reviews require the amber label.','-kind','rule','-scope','repo',
                      '-source','user','-expires','2099-12-31')
        # Every call is a fresh OS process, as later sessions would be.
        result=self.search('-kind','rule,decision','synthetic orchard')
        self.assertEqual([x['id'] for x in result['facts']],[fact])
        hit=result['facts'][0]
        self.assertEqual(hit['scope'],'repo:synthetic.invalid/team/garden')
        self.assertIn('synthetic.invalid/team/garden',hit['origin'])
        self.assertEqual(hit['expires'],'2099-12-31')
        self.assertNotIn('source',hit)
        self.assertNotIn('kind',hit)
        self.cli('correct',fact,'Synthetic orchard reviews require the violet label.')
        self.assertIn('violet',self.search('synthetic orchard')['facts'][0]['content'])
        self.cli('retire',fact)
        self.assertEqual(self.search('synthetic orchard')['facts'],[])
        history=[f for f in self.dump() if f['id']==fact]
        self.assertEqual([f['version'] for f in history],[1,2,3])
        self.assertEqual([f['status'] for f in history],['active','active','retired'])
        for f in history:
            self.assertEqual((f['source'],f['kind'],f['scope'],f['expires']),
                             ('user','rule','repo:synthetic.invalid/team/garden','2099-12-31'))
        self.assertIn('amber',history[0]['content'])
        self.assertIn('violet',history[-1]['content'])
        self.assertFalse((self.home/'unexpected').exists())

    def test_repository_global_host_shelves_expiry_and_non_git_cwd(self):
        repo=self.add('Synthetic orchard repository rule.','-kind','rule','-source','user','-scope','repo')
        global_id=self.add('Synthetic orchard global rule.','-kind','rule','-source','user','-scope','global','-origin','_global 2026-01-01')
        host=self.add('Synthetic orchard host rule.','-kind','rule','-source','user','-scope','host:synthetic-host','-origin','synthetic-host 2026-01-01')
        other=self.add('Synthetic orchard other repository rule.','-kind','rule','-source','user','-scope','repo','-origin','synthetic.invalid/other 2026-01-01')
        shelf=self.add('Synthetic orchard research claim. Source: synthetic manual.','-shelf','research','-source','tool')
        expired=self.add('Synthetic orchard old event.','-kind','event','-source','user','-scope','global','-expires','2000-01-01')
        ids=lambda result:{f['id'] for f in result['facts']}
        self.assertEqual(ids(self.search('synthetic orchard')),{repo,global_id,host})
        self.assertEqual(ids(self.search('-in','project','synthetic orchard')),{repo})
        self.assertEqual(ids(self.search('-in','global','synthetic orchard')),{global_id})
        self.assertEqual(ids(self.search('-in','host','synthetic orchard')),{host})
        self.assertEqual(ids(self.search('-in','shelf:research','synthetic orchard')),{shelf})
        self.assertIn(expired,ids(self.search('-expired','synthetic orchard')))
        self.assertIn(other,ids(self.search('-in','all','synthetic orchard')))
        # / is a real non-Git cwd. Only the configured synthetic base/state are written; global Git config is disabled.
        self.assertEqual(ids(self.search('synthetic orchard',cwd='/')),{repo,global_id,host,other})
        r=self.cli('add','-scope','repo','-source','user','--','Synthetic root-directory preference.',cwd='/')
        root_id=r.stdout.strip()
        self.assertEqual(next(f for f in self.dump() if f['id']==root_id)['scope'],'dir:/')
        hints=self.search('research claim')
        self.assertEqual(hints['facts'],[])
        self.assertTrue(any('shelf' in h.lower() for h in hints.get('hints',[])))

    def test_hook_retrieves_no_facts_and_dream_prepare_apply_rejects_stale_packet(self):
        fact=self.add('Synthetic orchard reviews require the amber label.','-kind','rule','-source','user','-scope','repo')
        payload=json.dumps({'hook_event_name':'SessionStart','source':'compact','session_id':'synthetic-session',
                            'cwd':str(self.nested),'model':'synthetic-model','permission_mode':'default','transcript_path':None})
        (self.home/'state/dream.at').write_text('2000-01-01T00:00:00Z\n')
        response=json.loads(self.cli('hook',data=payload).stdout)
        note=response['hookSpecificOutput']['additionalContext']
        self.assertEqual(response['hookSpecificOutput']['hookEventName'],'SessionStart')
        self.assertIn('facts wait for the dream',note)
        self.assertNotIn('amber label',note)
        self.assertNotIn('memcue_dream_prepare',note)
        self.assertFalse((self.home/'state/dream.started').exists())
        self.cli('dream','-prepare')
        packet=json.loads((self.home/'state/dream.packet.json').read_text())
        answer=self.home/'answer.json'
        answer.write_text(json.dumps({'packet':packet['id'],'splits':[],
                                      'actions':[],'scopes':[]}))
        before=self.dump()
        obsolete=self.home/'obsolete-answer.json'
        obsolete.write_text(json.dumps({'packet':packet['id'],
            'splits':[{'id':fact,'parts':['Synthetic orchard reviews require labels.','The label is amber.'],'reason':'fixture'}],
            'actions':[],'scopes':[],'expansions':[{'id':fact,'queries':['obsolete marker']}]}))
        refused=self.cli('dream','-apply',str(obsolete),ok=False)
        self.assertIn('unknown field',refused.stderr)
        self.assertEqual(self.dump(),before)
        r=self.cli('dream','-apply',str(answer))
        self.assertNotIn('queries',[f for f in self.dump() if f['id']==fact][-1])
        self.add('Synthetic orchard labels remain visible.','-kind','rule','-source','user','-scope','repo')
        self.cli('dream','-prepare')
        r=self.cli('dream','-apply',str(answer),ok=False)
        self.assertIn('run -prepare again',r.stderr)
        self.assertFalse((self.home/'unexpected').exists())

    def first_dream(self, *initialization, startup=False):
        self.assertFalse((self.home/'facts').exists())
        self.assertFalse((self.home/'state').exists())
        self.cli('instructions')  # Hooks-disabled fallback must need no maintenance hook.
        if startup:
            payload=json.dumps({'hook_event_name':'SessionStart','source':'startup','session_id':'synthetic-first',
                                'cwd':str(self.nested),'model':'synthetic-model','permission_mode':'default',
                                'transcript_path':None})
            self.cli('hook',data=payload)
            self.assertFalse((self.home/'facts').exists())
        self.assertEqual(self.search('synthetic orchard')['facts'],[])
        fact=self.add('Synthetic orchard reviews require the amber label.','-kind','rule','-source','user','-scope','repo')
        schedule=self.home/'state/dream.at'
        packet_path=self.home/'state/dream.packet.json'
        self.assertFalse(schedule.exists())
        for _ in range(2):
            self.assertIn('no dream yet',self.cli('dream','-prepare',ok=False).stderr)
        self.assertFalse(packet_path.exists())
        prepared=self.cli('dream','-prepare',*initialization)
        packet=json.loads(packet_path.read_text())
        self.assertIn(packet['id'],prepared.stdout)
        # First-dream initialization remains independent of removed translation work (#669).
        self.assertNotIn('"translations"',prepared.stdout)
        self.assertNotIn('## Данные: перевод',prepared.stdout)
        self.assertNotIn('translate',packet)
        self.assertEqual(packet['seen'][fact],1)
        self.assertEqual(packet['split'][fact],1)
        self.assertNotIn('expand',packet)
        self.assertFalse(schedule.exists())  # Prepare keeps a packet; apply completes the native dream.
        answer=self.home/'first-answer.json'
        answer.write_text(json.dumps({'packet':packet['id'],'splits':[],
                                      'actions':[],'scopes':[]}))
        self.cli('dream','-apply',str(answer))  # Fresh process reads the persisted packet.
        self.assertTrue(schedule.is_file())
        latest=[f for f in self.dump() if f['id']==fact][-1]
        self.assertNotIn('queries',latest)
        self.assertNotIn('keywords',latest)
        self.assertEqual(latest['source'],'user')
        log=[json.loads(line) for line in (self.home/'state/dream.log').read_text().splitlines()]
        self.assertEqual(log[-1]['packet'],packet['id'])
        self.assertNotIn('expanded',log[-1])
        self.add('Synthetic orchard labels remain visible.','-kind','rule','-source','user','-scope','repo')
        self.cli('dream','-prepare')  # Subsequent dreams need no initialization flag.
        next_packet=json.loads(packet_path.read_text())
        self.assertNotEqual(next_packet['id'],packet['id'])
        history=[p.read_bytes() for p in sorted((self.home/'facts').glob('*/*.jsonl'))]
        rejected=self.cli('dream','-apply',str(answer),ok=False)
        self.assertIn('run -prepare again',rejected.stderr)
        self.assertEqual(history,[p.read_bytes() for p in sorted((self.home/'facts').glob('*/*.jsonl'))])
        self.assertFalse((self.home/'unexpected').exists())

    def test_fresh_hooks_disabled_first_dream_all(self):
        self.first_dream('-all')

    def test_fresh_hooks_disabled_first_dream_since_date(self):
        self.first_dream('-since','2000-01-01')

    def test_startup_before_base_exists_first_dream_since_timestamp(self):
        self.first_dream('-since','2000-01-01T00:00:00Z',startup=True)

    def test_first_dream_since_validation_remains_native(self):
        self.add('Synthetic orchard label.','-kind','rule','-source','user','-scope','repo')
        for date in ('not-a-date','2026-02-30','2000-01-01T25:00:00Z'):
            with self.subTest(date=date):
                error=self.cli('dream','-prepare','-since',date,ok=False).stderr
                self.assertIn('memcue command failed',error)
                self.assertIn('want YYYY-MM-DD or an RFC 3339 time',error)
                self.assertFalse((self.home/'state/dream.at').exists())
                self.assertFalse((self.home/'state/dream.packet.json').exists())
        self.assertFalse((self.home/'unexpected').exists())

    def test_denied_write_is_visible_without_changing_configuration_or_history(self):
        # A regular file where a parent directory is required is deterministic, even as root.
        (self.home/'blocked').write_text('unchanged')
        self.settings['facts']=str(self.home/'blocked/facts')
        self.config.write_text(json.dumps(self.settings))
        r=self.cli('add','-source','user','--','Synthetic blocked preference.',ok=False)
        self.assertIn('memcue command failed',r.stderr)
        self.assertLess(len(r.stderr),750)
        self.assertEqual((self.home/'blocked').read_text(),'unchanged')
        self.assertEqual(json.loads(self.config.read_text()),self.settings)
        self.assertFalse((self.home/'.claude').exists())
        self.assertEqual(subprocess.run([sys.executable,'-c','print(2+2)'],capture_output=True,text=True).stdout,'4\n')

    @unittest.skipIf(hasattr(os,'geteuid') and os.geteuid()==0,'permission denial requires an unprivileged user')
    def test_unwritable_base_reports_permission_denial(self):
        restricted=self.home/'restricted'
        restricted.mkdir(mode=0o500)
        self.addCleanup(restricted.chmod,0o700)
        self.settings['facts']=str(restricted/'facts')
        self.config.write_text(json.dumps(self.settings))
        r=self.cli('add','-source','user','--','Synthetic permission preference.',ok=False)
        self.assertIn('permission denied',r.stderr.lower())
        self.assertLess(len(r.stderr),750)
        self.assertEqual(list(restricted.iterdir()),[])

    def test_installed_copy_runs_without_repository_files(self):
        installed=self.home/'standalone plugin'
        shutil.copytree(ROOT,installed)
        r=subprocess.run([sys.executable,'-I',str(installed/'scripts/memcue.py'),'--config',str(self.config),'instructions'],
                         cwd=self.nested,env=self.env,capture_output=True,text=True,timeout=20)
        self.assertEqual(r.returncode,0,r.stderr)
        self.assertIn(str(installed/'skills/memcue/SKILL.md'),r.stdout)


if __name__ == '__main__':
    unittest.main()
