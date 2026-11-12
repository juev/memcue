"""Real released normal-plugin discovery and request dispatch; synthetic storage only."""
import os,sys,json,tempfile,subprocess,shutil,unittest,copy
from pathlib import Path
if os.environ.get('MEMCUE_HERMES_DISTRIBUTION'):
 sys.path.insert(0,os.environ['MEMCUE_HERMES_DISTRIBUTION'])
ROOT=Path(__file__).resolve().parent
OPEN='[memcue-context]';CLOSE='[/memcue-context]'
SNAPSHOT={'context':'Current repository pitfalls for this checkout:','facts':['Failure-0 cause; full workaround.']}

def is_card(part):
 return type(part).__name__=='_TransientContext'

def user_text(message):
 """A user message with the user's own text: the slot of the current turn."""
 if not isinstance(message,dict) or message.get('role')!='user' or message.get('type') not in (None,'message'):return False
 c=message.get('content')
 return isinstance(c,str) or (isinstance(c,list) and any(isinstance(x,dict) and isinstance(x.get('text'),str) for x in c))

def target(payload,key):
 items=payload.get(key) or []
 return next((i for i in range(len(items)-1,-1,-1) if user_text(items[i])),None)

def card(payload,key):
 """The framed card this payload delivers in its current-turn user message, or None."""
 i=target(payload,key)
 if i is None:return None
 c=payload[key][i]['content']
 if isinstance(c,str):return c[c.rfind(OPEN):] if c.endswith(CLOSE) and OPEN in c else None
 parts=[x for x in c if is_card(x)]
 return parts[-1]['text'] if parts else None

def bare(payload,key):
 """The same payload with this plugin's card removed from the current-turn user message."""
 out=copy.deepcopy(payload);i=target(out,key)
 if i is None:return out
 m=dict(out[key][i]);c=m['content']
 if isinstance(c,str):
  if c.endswith('\n'+CLOSE):
   at=c.rfind('\n\n'+OPEN+'\n')
   c=c[:at] if at>=0 else ('' if c.startswith(OPEN+'\n') else c)
 else:c=[x for x in c if not is_card(x)]
 m['content']=c;out[key]=[*out[key][:i],m,*out[key][i+1:]]
 return out

USER={'messages':[{'role':'user','content':'ok'}]}

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
     # The card rides in the current turn's user message; prompt, roles and length stay as sent.
     self.assertEqual(len(result.payload[key]),len(request[key]),'the card added a message')
     self.assertEqual([m.get('role') for m in result.payload[key]],[m.get('role') for m in request[key]])
     for field in ('instructions','system'):self.assertEqual(result.payload.get(field),request.get(field))
     self.assertEqual(result.payload[key][:-1],request[key][:-1],'history before the current turn changed')
     sent=card(result.payload,key)
     self.assertIsNotNone(sent,'the card is not in the current-turn user message')
     self.assertTrue(result.payload[key][-1]['content'].startswith('ok\n\n'+OPEN))
     self.assertIn(f'Failure-{len(repos)-1} cause; full workaround.',sent)
     self.assertNotIn('Failure-0 cause;',sent)
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

      # Identical ordinary framing/text is not provenance: earlier turns, tool output, a plain part
      # with the same text, and a frame the user wrote before more text all stay as sent.
      ordinary=card(changed,key)
      if key=='messages':
       history=[{'role':'user','content':ordinary},{'role':'assistant','content':None,'tool_calls':[{'id':'same','type':'function','function':{'name':'terminal','arguments':'{}'}}]},{'role':'tool','tool_call_id':'same','content':ordinary},{'role':'user','content':[{'type':'text','text':ordinary}]},{'role':'assistant','content':'done'},{'role':'user','content':ordinary+'\n\nfollow-up'}]
       ordinary_request={key:history}
      else:
       history=[{'type':'message','role':'user','content':[{'type':'input_text','text':ordinary}]},{'type':'function_call','call_id':'same','name':'terminal','arguments':'{}'},{'type':'function_call_output','call_id':'same','output':json.dumps(ordinary)},{'type':'message','role':'user','content':[{'type':'input_text','text':ordinary}]}]
       ordinary_request={'model':'synthetic','instructions':'original instructions',key:history}
      ordinary_before=copy.deepcopy(ordinary_request)
      ordinary_sent=apply_llm_request_middleware(ordinary_request,session_id='owned-session',api_mode=mode).payload
      self.assertEqual(bare(ordinary_sent,key)[key],history,'ordinary identical text was mistaken for owned transient context')
      self.assertIn('Failure-0 cause;',card(ordinary_sent,key),'the current-turn user message did not get the card')
      self.assertEqual(ordinary_request,ordinary_before)
     finally:clear_session_cwd()
     unrelated=apply_llm_request_middleware(request,session_id='unrelated',api_mode=mode)
     self.assertEqual(unrelated.payload,request)
    # Protocol and failure controls run through the real released dispatcher.
    from unittest.mock import patch
    token=set_session_cwd(str(repos[0]))
    try:
     protocol={'messages':[{'role':'system','content':'original system'},{'role':'user','content':'ok'},{'role':'assistant','content':None,'tool_calls':[{'id':'call-1','type':'function','function':{'name':'terminal','arguments':'{}'}}]},{'role':'tool','tool_call_id':'call-1','content':'original tool output'}]}
     # Mid-turn, after tool calls: the card stays in the turn's user message, ahead of the tool chain,
     # and the tool output the model reads is the one the tool returned.
     sent=apply_llm_request_middleware(protocol,session_id='owned-session',api_mode='chat').payload
     self.assertEqual(len(sent['messages']),len(protocol['messages']))
     self.assertEqual(sent['messages'][0],protocol['messages'][0])
     self.assertEqual(sent['messages'][2:],protocol['messages'][2:])
     self.assertTrue(sent['messages'][1]['content'].startswith('ok\n\n'+OPEN))
     self.assertIn('Failure-0 cause;',sent['messages'][1]['content'])
     self.assertEqual(bare(sent,'messages'),protocol)
     response={'model':'synthetic','instructions':'original instructions','input':[{'role':'user','content':'ok'},{'type':'function_call','call_id':'call-1','name':'terminal','arguments':'{}'},{'type':'function_call_output','call_id':'call-1','output':'original function output'}]}
     sent=apply_llm_request_middleware(response,session_id='owned-session',api_mode='codex_responses').payload
     self.assertEqual(sent['instructions'],response['instructions'])
     self.assertEqual(sent['input'][1:],response['input'][1:])
     self.assertIn('Failure-0 cause;',sent['input'][0]['content'])
     self.assertEqual(bare(sent,'input'),response)
     # Tool output alone carries no user text: there is no current-turn slot, so nothing changes.
     tools_only={**response,'input':response['input'][1:]}
     self.assertEqual(apply_llm_request_middleware(tools_only,session_id='owned-session',api_mode='codex_responses').payload,tools_only)
     with patch.object(provider,'_run',return_value=(False,'synthetic transport failure')):
      failed=apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat').payload
      self.assertIn('unavailable',json.dumps(failed));self.assertNotIn('Failure-0 cause;',json.dumps(failed))
     provider.on_pre_compress([])
     recovered=apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat').payload
     self.assertIn('Failure-0 cause;',json.dumps(recovered))
     sub=load_memory_provider('memcue');sub.initialize('sub-session',hermes_home=tmp,agent_context='subagent')
     self.assertEqual(apply_llm_request_middleware(USER,session_id='sub-session',api_mode='chat').payload,USER)
     config=(home/'config.yaml').read_text()
     (home/'config.yaml').write_text(config.replace('provider: memcue','provider: builtin'))
     self.assertEqual(apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat').payload,USER)
     (home/'config.yaml').write_text(config.replace('enabled: [memcue-context]','enabled: []'))
     manager.discover_and_load(force=True)
     self.assertEqual(apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat').payload,USER)
     (home/'config.yaml').write_text(config);manager.discover_and_load(force=True)
    finally:clear_session_cwd()
    token=set_session_cwd(str(repos[0]))
    def revoke_during_read(*args,**kwargs):
     provider.on_session_switch('during-read')
     return True,json.dumps({'context':'Revoked snapshot','facts':['Revoked failure; full workaround.']})
    try:
     with patch.object(provider,'_run',side_effect=revoke_during_read):
      revoked=apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat')
      self.assertEqual(revoked.payload,USER,'ownership revoked during native I/O still injected facts')
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
     b=apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat')
     self.assertIn('Profile-B-only cause;',json.dumps(b.payload))
     self.assertNotIn('Failure-0 cause;',json.dumps(b.payload),'profile A facts leaked through shared native state')
    finally:reset_hermes_home_override(profile_token);clear_session_cwd()
    token=set_session_cwd(str(repos[0]))
    try:
     a=apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat')
     self.assertNotIn('Profile-B-only cause;',json.dumps(a.payload),'profile B facts leaked into profile A')
    finally:clear_session_cwd()
    provider.on_session_switch('rotated-session')
    token=set_session_cwd(str(repos[0]))
    try:
     rotated=apply_llm_request_middleware(USER,session_id='rotated-session',api_mode='chat')
     self.assertIn('Failure-0 cause;',json.dumps(rotated.payload),'rotated provider ownership lost automatic context')
     self.assertEqual(apply_llm_request_middleware(USER,session_id='owned-session',api_mode='chat').payload,USER)
     provider.shutdown()
     self.assertEqual(apply_llm_request_middleware(USER,session_id='rotated-session',api_mode='chat').payload,USER)
    finally:clear_session_cwd()

 def test_every_released_transport_takes_the_card_in_the_current_turn_user_message(self):
  """A middleware payload is transport-shaped already: the card joins the turn's own user text."""
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
    def unchanged_but(out,payload,key,index):
     """Everything but the target message is byte-identical; the target only gains the card."""
     self.assertEqual({k:v for k,v in out.items() if k!=key},{k:v for k,v in payload.items() if k!=key},'the prompt slot changed')
     self.assertEqual(len(out[key]),len(payload[key]),'the card added a message')
     for i,(a,b) in enumerate(zip(out[key],payload[key])):
      if i!=index:self.assertEqual(a,b,f'message {i} outside the current turn changed')
     self.assertEqual(target(out,key),index)
     self.assertEqual(bare(out,key),payload)
     self.assertIn('Failure-0 cause;',card(out,key))
     self.assertEqual(send(copy.deepcopy(out),mode),out,'a retry stacked the card')
    tool_chain=[{'role':'assistant','content':None,'tool_calls':[{'id':'c1','type':'function','function':{'name':'terminal','arguments':'{}'}}]},{'role':'tool','tool_call_id':'c1','content':'tool output'}]
    try:
     with patch.object(provider,'_run',return_value=(True,json.dumps(SNAPSHOT))):
      # Chat completions: no new message and no role the payload did not already send. A provider
      # that accepts a system message only at the start never sees a trailing one.
      for role in ('system','developer'):
       mode='chat_completions'
       chat={'model':'synthetic','messages':[{'role':role,'content':'Base instructions.'},{'role':'user','content':'ok'},*tool_chain]}
       out=send(copy.deepcopy(chat),mode)
       unchanged_but(out,chat,'messages',1)
       self.assertEqual(out['messages'][1]['content'][:4],'ok\n\n')
       self.assertEqual([m['role'] for m in out['messages']],[m['role'] for m in chat['messages']])
      # OpenRouter-style parts with a cache breakpoint: one more part of the same type, no breakpoint.
      parts={'model':'synthetic','messages':[{'role':'system','content':'Base instructions.'},{'role':'user','content':[{'type':'text','text':'ok','cache_control':{'type':'ephemeral'}}]}]}
      out=send(copy.deepcopy(parts),mode)
      unchanged_but(out,parts,'messages',1)
      self.assertEqual(out['messages'][1]['content'][0],parts['messages'][1]['content'][0])
      self.assertEqual({k:v for k,v in out['messages'][1]['content'][1].items() if k!='text'},{'type':'text'},'a fresh part must not inherit the old cache breakpoint')
      # Codex Responses: `instructions` and every non-user item stay; the card passes the released
      # preflight, while a developer message item still raises there before the call leaves.
      import inspect
      from agent.transports.codex import ResponsesApiTransport
      transport=ResponsesApiTransport()
      def preflight(payload):
       flags={'sanitize_harmony_tokens':True} if 'sanitize_harmony_tokens' in inspect.signature(transport.preflight_kwargs).parameters else {}
       return transport.preflight_kwargs(copy.deepcopy(payload),allow_stream=False,**flags)
      mode='codex_responses'
      codex={'model':'gpt-6.1-sol','instructions':'Base instructions.','input':[{'role':'user','content':'ok'},{'type':'function_call','call_id':'c1','name':'terminal','arguments':'{}'},{'type':'function_call_output','call_id':'c1','output':'tool output'}],'store':False}
      out=send(copy.deepcopy(codex),mode)
      unchanged_but(out,codex,'input',0)
      checked=preflight(out)
      self.assertEqual(checked['instructions'],'Base instructions.')
      self.assertIn('Failure-0 cause;',json.dumps(checked['input']),'the released preflight dropped the card')
      with self.assertRaises(ValueError):
       preflight({**codex,'input':[*codex['input'],{'type':'message','role':'developer','content':[{'type':'input_text','text':'card'}]}]})
      typed={**codex,'input':[{'type':'message','role':'user','content':[{'type':'input_text','text':'ok'}]}]}
      out=send(copy.deepcopy(typed),mode)
      unchanged_but(out,typed,'input',0)
      self.assertEqual(set(out['input'][0]['content'][1]),{'type','text'})
      self.assertEqual(out['input'][0]['content'][1]['type'],'input_text')
      # Anthropic messages from the released adapter: `system` stays, the card goes into the turn's
      # user text, never into the tool_result carrier that follows the tool call.
      from agent.anthropic_adapter import build_anthropic_kwargs
      def anthropic_kwargs(messages):
       wanted={'model':'claude-opus-4.8','messages':messages,'tools':None,'max_tokens':None,'reasoning_config':None}
       params=inspect.signature(build_anthropic_kwargs).parameters
       return build_anthropic_kwargs(**{k:v for k,v in wanted.items() if k in params})
      mode='anthropic_messages'
      akw=anthropic_kwargs([{'role':'system','content':'Base instructions.'},{'role':'user','content':'ok'},*tool_chain])
      self.assertEqual(akw['messages'][-1]['content'][0]['type'],'tool_result')
      out=send(copy.deepcopy(akw),mode)
      unchanged_but(out,akw,'messages',0)
      blocks={'model':'claude-opus-4.8','max_tokens':64,'system':[{'type':'text','text':'Base instructions.','cache_control':{'type':'ephemeral'}}],'messages':[{'role':'user','content':[{'type':'text','text':'ok','cache_control':{'type':'ephemeral'}}]}]}
      out=send(copy.deepcopy(blocks),mode)
      unchanged_but(out,blocks,'messages',0)
      self.assertEqual({k:v for k,v in out['messages'][0]['content'][1].items() if k!='text'},{'type':'text'})
      # Bedrock Converse from the released adapter: bare text blocks, toolResult untouched.
      from agent.bedrock_adapter import convert_messages_to_converse
      mode='bedrock_converse'
      system_blocks,converse=convert_messages_to_converse([{'role':'system','content':'Base instructions.'},{'role':'user','content':'ok'},*tool_chain])
      bedrock={'model':'synthetic','messages':converse,'system':system_blocks}
      out=send(copy.deepcopy(bedrock),mode)
      unchanged_but(out,bedrock,'messages',0)
      self.assertEqual(set(out['messages'][0]['content'][1]),{'text'})
      # No user text anywhere: send it back untouched instead of inventing a slot.
      for payload,mode in (({'contents':[{'role':'user','parts':[{'text':'ok'}]}],'systemInstruction':{'parts':[{'text':'Base'}]}},'gemini_native'),
                           ({'model':'synthetic','instructions':'Base','input':codex['input'][1:]},'codex_responses'),
                           ({'model':'claude-opus-4.8','max_tokens':64,'messages':akw['messages'][1:]},'anthropic_messages'),
                           ({'model':'synthetic','messages':[{'role':'system','content':'Base'}]},'chat_completions')):
       before=copy.deepcopy(payload)
       self.assertEqual(send(copy.deepcopy(payload),mode),before,'a payload with no user text was rewritten anyway')
    finally:clear_session_cwd();provider.shutdown()

if __name__=='__main__':
 unittest.main()
