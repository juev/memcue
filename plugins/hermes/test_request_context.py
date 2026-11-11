"""Real released normal-plugin discovery and request dispatch; synthetic storage only."""
import os,sys,json,tempfile,subprocess,shutil,unittest,copy
from pathlib import Path
if os.environ.get('MEMCUE_HERMES_DISTRIBUTION'):
 sys.path.insert(0,os.environ['MEMCUE_HERMES_DISTRIBUTION'])
ROOT=Path(__file__).resolve().parent
OPEN='[memcue-context]';CLOSE='[/memcue-context]'
SNAPSHOT={'context':'Current repository pitfalls for this checkout:','facts':['Failure-0 cause; full workaround.']}

def injected(payload,key):
 """This plugin's own item, as plain data, for a payload sent in `key`'s shape."""
 if key=='messages':return [dict(m) for m in payload[key] if type(m).__name__=='_TransientContext']
 return [dict(b) for b in payload.get('system') or [] if isinstance(b,dict) and isinstance(b.get('text'),str) and OPEN in b['text']]

def card(payload,key):
 """What this payload delivers as the card, or None when its shape carries none."""
 if isinstance(payload.get('instructions'),str) and OPEN in payload['instructions']:return payload['instructions']
 system=payload.get('system')
 if isinstance(system,str) and OPEN in system:return system
 if isinstance(system,list):return json.dumps(system,ensure_ascii=False) if OPEN in json.dumps(system) else None
 items=injected(payload,key)
 return json.dumps(items,ensure_ascii=False) if items else None

def bare(payload,key):
 """The same payload with this plugin's card removed; separators and block lists normalized."""
 out=copy.deepcopy(payload)
 if isinstance(out.get('instructions'),str) and OPEN in out['instructions']:
  out['instructions']=out['instructions'][:out['instructions'].index(OPEN)].rstrip()
  return out
 system=out.get('system')
 if isinstance(system,str) and OPEN in system:
  out['system']=system[:system.index(OPEN)].rstrip();return out
 if isinstance(system,list):
  out['system']=[b for b in system if not (isinstance(b,dict) and isinstance(b.get('text'),str) and OPEN in b['text'])]
  return out
 out[key]=[m for m in out.get(key) or [] if type(m).__name__!='_TransientContext']
 return out

def henv(tmp):
 home=Path(tmp)
 env=dict(os.environ)
 for k in list(env):
  if k.startswith(('MEMCUE_','CLAUDE_','ANTHROPIC_')):env.pop(k)
 env.update(HOME=tmp,HERMES_HOME=tmp,XDG_STATE_HOME=str(home/'state'),XDG_CONFIG_HOME=str(home/'config'),MEMCUE_FACTS=str(home/'facts'),MEMCUE_STATE_DIR=str(home/'native-state'),MEMCUE_DREAM_BACKGROUND='0',MEMCUE_AUTO_INSTALL='0',GIT_CONFIG_GLOBAL='/dev/null',GIT_CONFIG_SYSTEM='/dev/null',GIT_CONFIG_NOSYSTEM='1')
 return home,env

def install(home,binary):
 for name in ('memcue','memcue-context'):shutil.copytree(ROOT/name,home/'plugins'/name)
 (home/'config.yaml').write_text('memory:\n  provider: memcue\n  memcue:\n    binary: '+json.dumps(binary)+'\n    dream: false\nplugins:\n  enabled: [memcue-context]\n')

class RequestContext(unittest.TestCase):
 def test_actual_normal_activation_and_same_turn_cwd(self):
  binary=os.environ['MEMCUE_BIN']
  with tempfile.TemporaryDirectory(prefix='memcue-request-') as tmp:
   home,env=henv(tmp)
   from unittest.mock import patch
   with patch.dict(os.environ,env,clear=True):
    install(home,binary)
    from hermes_cli.plugins import get_plugin_manager
    from hermes_cli.middleware import apply_llm_request_middleware
    from plugins.memory import load_memory_provider
    from agent.runtime_cwd import set_session_cwd,clear_session_cwd
    manager=get_plugin_manager();manager.discover_and_load(force=True)
    provider=load_memory_provider('memcue')
    self.assertIsNotNone(provider)
    provider.initialize('owned-session',hermes_home=tmp,cwd=tmp,agent_context='primary')
    repos=[]
    for i in range(2):
     repo=home/str(i);subprocess.run(['git','init','-q',str(repo)],check=True)
     subprocess.run(['git','-C',str(repo),'remote','add','origin',f'https://example.invalid/{i}.git'],check=True)
     subprocess.run([binary,'add','-cwd',str(repo),'-kind','pitfall','-scope','repo','--',f'Failure-{i} cause; full workaround.'],check=True,stdout=subprocess.DEVNULL)
     repos.append(repo)
    provider.initialize('owned-session',hermes_home=tmp,cwd=str(repos[0]),agent_context='primary')
    token=set_session_cwd(str(repos[0]))
    try:
     self.assertNotIn('Failure-0 cause;',provider.system_prompt_block(),'middleware mode duplicated facts in cached system prompt')
     self.assertEqual(provider.prefetch('ok',session_id='owned-session'),'','middleware mode duplicated turn-entry delivery')
    finally:clear_session_cwd()
    # Every shape a released provider can send in the same turn, with the role that payload uses:
    # a chat-completions prompt keeps its own leading role, a Responses prompt takes instructions.
    for key,shape,role in (
     ('messages',{'model':'synthetic','messages':[{'role':'system','content':'Base instructions.'},{'role':'user','content':'ok'}]},'system'),
     ('messages',{'model':'synthetic','messages':[{'role':'developer','content':'Base instructions.'},{'role':'user','content':'ok'}]},'developer'),
     ('messages',{'model':'synthetic','messages':[{'role':'user','content':'ok'}]},'system'),
     ('input',{'model':'synthetic','instructions':'Base instructions.','input':[{'role':'user','content':'ok'}]},None)):
     mode='chat' if key=='messages' else 'codex_responses'
     request=copy.deepcopy(shape);original=copy.deepcopy(request)
     for i,repo in enumerate(repos):
      token=set_session_cwd(str(repo))
      try:result=apply_llm_request_middleware(request,session_id='owned-session',turn_id='same-turn',api_mode=mode)
      finally:clear_session_cwd()
      text=json.dumps(result.payload,ensure_ascii=False)
      self.assertIn(f'Failure-{i} cause; full workaround.',text,'per-request middleware omitted actual cwd pitfalls in the same turn')
      self.assertNotIn(f'Failure-{1-i} cause;',text)
      self.assertEqual(request,original)
      self.assertEqual(set(result.payload),set(request),'private request metadata escaped')
      self.assertEqual(bare(result.payload,key),bare(request,key),'the card displaced the payload it was added to')
     if key=='messages':
      self.assertEqual(len(injected(result.payload,key)),1,'more than one card message was appended')
      item=injected(result.payload,key)[-1]
      self.assertEqual(set(item),{'role','content'},'private marker serialized to provider')
      self.assertEqual(item['role'],role,'the card did not take the role this payload already sends')
      self.assertIn(f'Failure-{len(repos)-1} cause; full workaround.',item['content'])
      self.assertNotIn('Failure-0 cause;',item['content'])
     else:
      self.assertTrue(result.payload['instructions'].startswith('Base instructions.\n\n'+OPEN))
      self.assertEqual(result.payload['input'],request['input'])
     token=set_session_cwd(str(repos[1]))
     try:
      retry=apply_llm_request_middleware(result.payload,session_id='owned-session',turn_id='same-turn',api_mode=mode)
      self.assertEqual(retry.payload,result.payload,'retry duplicated transient repository context')
     finally:clear_session_cwd()
     # Accepted transformed-request extension: native retries normally use fresh kwargs.
     # Provenance must survive the released dispatcher's deep copies, outside model JSON.
     token=set_session_cwd(str(repos[0]))
     try:
      reused=copy.deepcopy(result.payload)
      before=copy.deepcopy(reused)
      changed=apply_llm_request_middleware(reused,session_id='owned-session',api_mode=mode).payload
      self.assertNotIn('Failure-1 cause;',json.dumps(changed),'transformed retry retained stale owned repo-B snapshot')
      self.assertIn('Failure-0 cause;',json.dumps(changed))
      self.assertEqual(bare(changed,key),bare(request,key),'a retry stacked the card instead of replacing it')
      self.assertEqual(reused,before,'transformed input was mutated')
      self.assertEqual(set(changed),set(request),'private request metadata escaped')
      with patch.object(provider,'_run',return_value=(False,'synthetic snapshot read failure')):
       failed=apply_llm_request_middleware(reused,session_id='owned-session',api_mode=mode).payload
      self.assertNotIn('Failure-1 cause;',json.dumps(failed),'failed transformed retry retained prior owned facts')
      self.assertIn('unavailable',json.dumps(failed))
      self.assertEqual(bare(failed,key),bare(request,key))
      recovered=apply_llm_request_middleware(failed,session_id='owned-session',api_mode=mode).payload
      self.assertEqual(recovered,changed,'successful retry did not recover one fresh snapshot')
      self.assertEqual(reused,before)

      # Identical ordinary framing/text is not provenance, including the last item.
      ordinary=card(changed,key)
      if key=='messages':
       item=injected(changed,key)[-1]
       history=[{'role':'user','content':item['content']},copy.deepcopy(item),{'role':'assistant','content':None,'tool_calls':[{'id':'same','type':'function','function':{'name':'terminal','arguments':'{}'}}]},{'role':'tool','tool_call_id':'same','content':item['content']},copy.deepcopy(item)]
       ordinary_request={key:history}
      else:
       history=[{'type':'message','role':'user','content':[{'type':'input_text','text':ordinary}]},{'type':'message','role':'user','content':[{'type':'input_text','text':ordinary}]},{'type':'function_call','call_id':'same','name':'terminal','arguments':'{}'},{'type':'function_call_output','call_id':'same','output':json.dumps(ordinary)}]
       ordinary_request={'model':'synthetic','instructions':'original instructions',key:history}
      ordinary_before=copy.deepcopy(ordinary_request)
      ordinary_sent=apply_llm_request_middleware(ordinary_request,session_id='owned-session',api_mode=mode).payload
      self.assertEqual(bare(ordinary_sent,key)[key],history,'ordinary identical text was mistaken for owned transient context')
      self.assertEqual(ordinary_request,ordinary_before)
     finally:clear_session_cwd()
     unrelated=apply_llm_request_middleware(request,session_id='unrelated',api_mode=mode)
     self.assertEqual(unrelated.payload,request)
    # Protocol and failure controls run through the real released dispatcher.
    from unittest.mock import patch
    token=set_session_cwd(str(repos[0]))
    try:
     protocol={'messages':[{'role':'system','content':'original system'},{'role':'user','content':'ok'},{'role':'assistant','content':None,'tool_calls':[{'id':'call-1','type':'function','function':{'name':'terminal','arguments':'{}'}}]},{'role':'tool','tool_call_id':'call-1','content':'original tool output'}]}
     sent=apply_llm_request_middleware(protocol,session_id='owned-session',api_mode='chat').payload
     self.assertEqual(sent['messages'][:-1],protocol['messages'])
     self.assertEqual(sent['messages'][-1]['role'],'system')
     self.assertIn('Failure-0 cause;',sent['messages'][-1]['content'])
     response={'model':'synthetic','instructions':'original instructions','input':[{'type':'function_call','call_id':'call-1','name':'terminal','arguments':'{}'},{'type':'function_call_output','call_id':'call-1','output':'original function output'}]}
     sent=apply_llm_request_middleware(response,session_id='owned-session',api_mode='codex_responses').payload
     self.assertEqual(sent['input'],response['input'])
     self.assertTrue(sent['instructions'].startswith('original instructions\n\n'+OPEN))
     self.assertIn('Failure-0 cause;',sent['instructions'])
     self.assertEqual(bare(sent,'input'),response)
     with patch.object(provider,'_run',return_value=(False,'synthetic transport failure')):
      failed=apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat').payload
      self.assertIn('unavailable',json.dumps(failed));self.assertNotIn('Failure-0 cause;',json.dumps(failed))
     provider.on_pre_compress([])
     recovered=apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat').payload
     self.assertIn('Failure-0 cause;',json.dumps(recovered))
     sub=load_memory_provider('memcue');sub.initialize('sub-session',hermes_home=tmp,agent_context='subagent')
     self.assertEqual(apply_llm_request_middleware({'messages':[]},session_id='sub-session',api_mode='chat').payload,{'messages':[]})
     config=(home/'config.yaml').read_text()
     (home/'config.yaml').write_text(config.replace('provider: memcue','provider: builtin'))
     self.assertEqual(apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat').payload,{'messages':[]})
     (home/'config.yaml').write_text(config.replace('enabled: [memcue-context]','enabled: []'))
     manager.discover_and_load(force=True)
     self.assertEqual(apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat').payload,{'messages':[]})
     (home/'config.yaml').write_text(config);manager.discover_and_load(force=True)
    finally:clear_session_cwd()
    token=set_session_cwd(str(repos[0]))
    def revoke_during_read(*args,**kwargs):
     provider.on_session_switch('during-read')
     return True,json.dumps({'context':'Revoked snapshot','facts':['Revoked failure; full workaround.']})
    try:
     with patch.object(provider,'_run',side_effect=revoke_during_read):
      revoked=apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat')
      self.assertEqual(revoked.payload,{'messages':[]},'ownership revoked during native I/O still injected facts')
    finally:provider.on_session_switch('owned-session');clear_session_cwd()
    # Another active profile with the same session/repository identity must retain its own base.
    from hermes_constants import set_hermes_home_override,reset_hermes_home_override
    other=home/'profile-b';other.mkdir()
    for name in ('memcue','memcue-context'):shutil.copytree(ROOT/name,other/'plugins'/name)
    (other/'config.yaml').write_text('memory:\n  provider: memcue\n  memcue:\n    binary: '+json.dumps(binary)+'\n    facts: '+json.dumps(str(other/'facts'))+'\n    dream: false\nplugins:\n  enabled: [memcue-context]\n')
    subprocess.run([binary,'add','-cwd',str(repos[0]),'-kind','pitfall','-scope','repo','--','Profile-B-only cause; private synthetic workaround.'],env=dict(env,MEMCUE_FACTS=str(other/'facts'),MEMCUE_STATE_DIR=str(other/'native-state')),check=True,stdout=subprocess.DEVNULL)
    profile_token=set_hermes_home_override(other)
    cwd_token=set_session_cwd(str(repos[0]))
    try:
     other_provider=load_memory_provider('memcue');other_provider.initialize('owned-session',hermes_home=str(other),agent_context='primary')
     b=apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat')
     self.assertIn('Profile-B-only cause;',json.dumps(b.payload))
     self.assertNotIn('Failure-0 cause;',json.dumps(b.payload),'profile A facts leaked through shared native state')
    finally:reset_hermes_home_override(profile_token);clear_session_cwd()
    token=set_session_cwd(str(repos[0]))
    try:
     a=apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat')
     self.assertNotIn('Profile-B-only cause;',json.dumps(a.payload),'profile B facts leaked into profile A')
    finally:clear_session_cwd()
    provider.on_session_switch('rotated-session')
    token=set_session_cwd(str(repos[0]))
    try:
     rotated=apply_llm_request_middleware({'messages':[]},session_id='rotated-session',api_mode='chat')
     self.assertIn('Failure-0 cause;',json.dumps(rotated.payload),'rotated provider ownership lost automatic context')
     self.assertEqual(apply_llm_request_middleware({'messages':[]},session_id='owned-session',api_mode='chat').payload,{'messages':[]})
     provider.shutdown()
     self.assertEqual(apply_llm_request_middleware({'messages':[]},session_id='rotated-session',api_mode='chat').payload,{'messages':[]})
    finally:clear_session_cwd()

 def test_every_released_transport_slot_takes_the_card_instead_of_a_new_role(self):
  """A middleware payload is transport-shaped already: the card goes where that transport keeps prompt text."""
  binary=os.environ['MEMCUE_BIN']
  with tempfile.TemporaryDirectory(prefix='memcue-slots-') as tmp:
   home,env=henv(tmp)
   from unittest.mock import patch
   with patch.dict(os.environ,env,clear=True):
    install(home,binary)
    from hermes_cli.plugins import get_plugin_manager
    from hermes_cli.middleware import apply_llm_request_middleware
    from plugins.memory import load_memory_provider
    from agent.runtime_cwd import clear_session_cwd
    manager=get_plugin_manager();manager.discover_and_load(force=True)
    provider=load_memory_provider('memcue');self.assertIsNotNone(provider)
    provider.initialize('slot-session',hermes_home=tmp,cwd=tmp,agent_context='primary')
    def send(payload,mode):return apply_llm_request_middleware(payload,session_id='slot-session',api_mode=mode).payload
    try:
     with patch.object(provider,'_run',return_value=(True,json.dumps(SNAPSHOT))):
      # Chat completions: one trailing message, in the role this payload already sends.
      for role in ('system','developer'):
       chat={'model':'synthetic','messages':[{'role':role,'content':'Base instructions.'},{'role':'user','content':'ok'}]}
       out=send(copy.deepcopy(chat),'chat_completions')
       self.assertEqual(out['messages'][:-1],chat['messages'])
       self.assertEqual(out['messages'][-1]['role'],role,'the card role must be one this payload already sends')
       self.assertIn('Failure-0 cause;',out['messages'][-1]['content'])
       self.assertTrue(all(m['role'] in {'system','developer','user','assistant','tool'} for m in out['messages']))
      # Codex Responses: only `instructions` takes system-level text. A message item with any role
      # but user/assistant raises in the released preflight, before the call ever leaves.
      import inspect
      from agent.transports.codex import ResponsesApiTransport
      transport=ResponsesApiTransport()
      def preflight(payload):
       flags={'sanitize_harmony_tokens':True} if 'sanitize_harmony_tokens' in inspect.signature(transport.preflight_kwargs).parameters else {}
       return transport.preflight_kwargs(copy.deepcopy(payload),allow_stream=False,**flags)
      codex={'model':'gpt-6.1-sol','instructions':'Base instructions.','input':[{'role':'user','content':'ok'}],'store':False}
      out=send(copy.deepcopy(codex),'codex_responses')
      self.assertEqual(out['input'],codex['input'])
      self.assertTrue(out['instructions'].startswith('Base instructions.\n\n'+OPEN))
      checked=preflight(out)
      self.assertIn('Failure-0 cause;',checked['instructions'],'the released preflight dropped the card')
      with self.assertRaises(ValueError):
       preflight({**codex,'input':[*codex['input'],{'type':'message','role':'developer','content':[{'type':'input_text','text':'card'}]}]})
      # Anthropic messages: prompt text lives outside `messages`, as a string or a block list.
      from agent.anthropic_adapter import build_anthropic_kwargs
      def anthropic_kwargs(messages):
       wanted={'model':'claude-opus-4.8','messages':messages,'tools':None,'max_tokens':None,'reasoning_config':None}
       params=inspect.signature(build_anthropic_kwargs).parameters
       return build_anthropic_kwargs(**{k:v for k,v in wanted.items() if k in params})
      akw=anthropic_kwargs([{'role':'system','content':'Base instructions.'},{'role':'user','content':'ok'}])
      out=send(copy.deepcopy(akw),'anthropic_messages')
      self.assertEqual(out['messages'],akw['messages'],'a `system` role among converted anthropic messages is rejected')
      self.assertTrue(out['system'].startswith('Base instructions.\n\n'+OPEN))
      blocks={'model':'claude-opus-4.8','max_tokens':64,'messages':[{'role':'user','content':[{'type':'text','text':'ok'}]}],'system':[{'type':'text','text':'Base instructions.','cache_control':{'type':'ephemeral'}}]}
      out=send(copy.deepcopy(blocks),'anthropic_messages')
      self.assertEqual(out['system'][0],blocks['system'][0])
      self.assertEqual({k:v for k,v in out['system'][1].items() if k!='text'},{'type':'text'},'a fresh block must not inherit the old cache breakpoint')
      self.assertIn('Failure-0 cause;',out['system'][1]['text'])
      self.assertEqual(send(copy.deepcopy(out),'anthropic_messages'),out,'a retry stacked anthropic system blocks')
      # Bedrock Converse: bare text blocks, no anthropic `type`.
      from agent.bedrock_adapter import convert_messages_to_converse
      system_blocks,converse=convert_messages_to_converse([{'role':'system','content':'Base instructions.'},{'role':'user','content':'ok'}])
      bedrock={'model':'synthetic','messages':converse,'system':system_blocks}
      out=send(copy.deepcopy(bedrock),'bedrock_converse')
      self.assertEqual(out['messages'],bedrock['messages'])
      self.assertEqual(set(out['system'][1]),{'text'})
      self.assertIn('Failure-0 cause;',out['system'][1]['text'])
      # No prompt slot, or a converted payload whose prompt text lives elsewhere: send it back
      # untouched. A role the target never accepted costs the whole turn on the primary.
      for payload,mode in (({'contents':[{'role':'user','parts':[{'text':'ok'}]}],'systemInstruction':{'parts':[{'text':'Base'}]}},'gemini_native'),
                           ({'model':'synthetic','input':[{'role':'user','content':'ok'}]},'codex_responses'),
                           ({'model':'claude-opus-4.8','max_tokens':64,'messages':[{'role':'user','content':[{'type':'text','text':'ok'}]}]},'anthropic_messages'),
                           ({'model':'synthetic','messages':[{'role':'user','content':'ok'}]},'anthropic_messages')):
       before=copy.deepcopy(payload)
       self.assertEqual(send(copy.deepcopy(payload),mode),before,'a payload with no prompt slot was rewritten anyway')
    finally:clear_session_cwd();provider.shutdown()

if __name__=='__main__':
 unittest.main()
