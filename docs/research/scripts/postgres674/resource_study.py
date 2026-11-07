"""One cached-vector resource observation, never another latency cohort."""
import time
ENTRY=time.monotonic()
import json,os,resource,shlex,subprocess,sys,traceback
from pathlib import Path
from ann_shape import candidate,reference,save
from ann_shape_exact import WholeBudget,Operations,Journal
from outputs import create_output_directory,write_new_text
from matched_query import safe_read,sha
import ann_shape_exact_study as old
from resource_probe import Sampler,remote_source,parse_sample,delta,install_guard,clear_guard

HERE=Path(__file__).absolute().parent;ROOT=HERE.parents[3];OWN=ROOT/'.research-private/local-resource-decision-01'
SOURCE=['resource_probe.py','resource_study.py','test_resource_probe.py','ann_shape.py','ann_shape_exact.py','ann_shape_exact_study.py','ann_shape_study.py','matched_query.py','backend.py','deployed.py','embed.py','outputs.py']
def hashes():return {n:sha(safe_read(HERE,n,set(SOURCE))) for n in SOURCE}
def read(p):return json.loads(safe_read(p.parent,p.name,{p.name}))
def target():
 r=read(OWN/'diagnosis/ssh-discovery.json');v=[x.split() for x in r['stdout'].splitlines()]
 assert r['exit']==0 and len(v)==1 and len(v[0])==3
 cid,name,image=v[0]
 minimal=read(OWN/'diagnosis/minimal-inspect.json');assert minimal['exit']==0
 meta=json.loads(minimal['stdout']);assert meta['id'].startswith(cid) and meta['name']=='/'+name and meta['status']=='running'
 return dict(host=r['argv'][-2],cid=cid,name=name,image=image,identity=meta)
def declare():
 assert subprocess.check_output(['git','rev-parse','HEAD'],text=True).strip()=='eed9196499096d1a5d7168e07c12c7e5230c15f6'
 t=target();d=old.load_inputs();src=create_output_directory(OWN/'source');h=hashes()
 for n in h:write_new_text(src/n,safe_read(HERE,n,set(h)).decode())
 queries=[]
 dep=old.Deployment(d['endpoint'],old.PREFIXES['30000'])
 for arm in ('reference','ann-40','ann-100'):
  for rep in range(2):
   for ordinal,c in enumerate(d['selected']):
    compiler=reference if arm=='reference' else candidate
    queries.append(dict(arm=arm,rep=rep,ordinal=ordinal,query=compiler(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']])))
 public=dict(status='PROSPECTIVE',source_hashes=h,baseline_s=20,sampling_interval_s=.5,remote_lifetime_s=90,whole_probe_wall_s=120,
 sql=dict(maximum=51,user_cap=64,settings_control=1,initial_fingerprint=1,resource_requests=48,final_fingerprint=1,startup_statements=0),
 workload='Same8cached contexts twice in saved order for natural reference, defaultANNef40, defaultANNef100;16pergroup,oneclient. No case/hyperparameter/filter/query/ranker/model changes.',
 measurement='Cgroup cumulative CPU seconds and current memory/anon/file/swap with boundaries pergroup;20second passive baseline. No latency quantiles or ANN cohort/quality extrapolation.',
 limits=dict(clients=1,query_timeout_s=5,parallel_workers=0,work_mem_mib=64,maintenance_work_mem_mib=64),
 safety='Readonly SSH with strict knownhost checking; one target only; no host/container/DB writes. Remote sampler exits on EOF/BrokenPipe and independent90s lifetime; own SSH reaped.',
 accounting='Container includes historical research tables/caches and background work; memory.current is not Docker working set or engine heap; no processRSS sums or historical memory.peak claims. SSH/host sampler overhead not PGcontainerCPU.',
 source_identity='Published ANN compiler bytes/bindings reused unchanged; old reports/seals remain immutable. New resource evidence does not replace latency measurements.',
 missing='Embeddingworker resources, intendedclient/network/toolpath, outages, production roles/batch/durablejobs and operating cost remain outside this probe.',new_embeddings=0,new_native_episodes=0,provider_calls=0)
 save(HERE/'resource-declaration.json',public)
 save(OWN/'private-declaration.json',dict(source_hashes=h,target=t,queries=queries,argv=sys.orig_argv,cli_thread_id=os.environ.get('CODEX_THREAD_ID'),public_sha256=sha((HERE/'resource-declaration.json').read_bytes())))
 print('DECLARED:51PGoperations maximum; passive20s;48fixed resource requests; no latency cohort.')

def execute():
 started=float(os.environ.get('MEMCUE_RESOURCE_STARTED',str(ENTRY)));budget=WholeBudget(started,seconds=120,ceiling=51)
 path=create_output_directory(OWN/'probe');op=Operations(path/'operations.jsonl',budget);records=Journal(path/'groups.jsonl');connections=Journal(path/'connections.jsonl')
 sampler=None;d=None;initial=None;ready=None;out=dict(status='BLOCKED_UNMEASURED',exit=1,groups=[])
 try:
  install_guard(started)
  budget.stage('load');cfg=read(OWN/'private-declaration.json');assert cfg['source_hashes']==hashes();d=old.load_inputs();budget.stage('sampler')
  t=cfg['target'];command='python3 -u -c '+shlex.quote(remote_source())+' '+' '.join(shlex.quote(t[k]) for k in ['cid','name','image'])
  argv=['ssh','-o','BatchMode=yes','-o','ConnectTimeout=5','-o','StrictHostKeyChecking=yes',t['host'],command]
  save(path/'sampler-command.json',dict(argv=argv,remote_lifetime_s=90))
  sampler=Sampler(argv,path);ready=sampler.receive('ready');assert ready['identity']==t['identity']
  with old.connect(d,budget,connections) as db:
   control=op.execute(db,"SELECT current_setting('default_transaction_read_only') readonly,current_setting('max_parallel_workers_per_gather') parallel,current_setting('work_mem') work_mem,current_setting('statement_timeout') timeout",phase='control',label=0)[0][0]
   assert control['readonly']=='on' and control['parallel']=='0';save(path/'settings.json',control)
   initial=op.execute(db,old.previous.fingerprints_sql(d),phase='initial-fingerprint',label=0)[0][0]['snapshot'];assert initial==d['snapshot'];save(path/'initial-snapshot.json',initial)
  before=sampler.boundary('baseline');time.sleep(20);after=sampler.boundary('baseline')
  record=dict(arm='baseline',before=before,after=after,delta=delta(before,after));records.write(record);out['groups'].append(dict(arm='baseline',**record['delta']))
  dep=old.Deployment(d['endpoint'],old.PREFIXES['30000'])
  for arm,ef in [('reference',40),('ann-40',40),('ann-100',100)]:
   budget.stage('group')
   with old.connect(d,budget,connections,ef) as db:
    before=sampler.boundary(arm)
    for row in cfg['queries']:
     if row['arm']!=arm:continue
     c=d['selected'][row['ordinal']];compiler=reference if arm=='reference' else candidate
     compiled=compiler(dep,d['norms'][c['id']],c['query'],d['vectors'][c['id']]);assert [compiled[0],compiled[1]]==row['query']
     op.execute(db,*compiled,phase='resource-request',label=[arm,row['rep'],row['ordinal']])
    after=sampler.boundary(arm)
   record=dict(arm=arm,before=before,after=after,delta=delta(before,after));records.write(record);out['groups'].append(dict(arm=arm,**record['delta']))
  out.update(status='MEASURED',exit=0)
 except BaseException as error:
  out.update(error_type=type(error).__name__);write_new_text(path/'failure.txt',traceback.format_exc())
 finally:
  clear_guard()
  if initial is not None:
   try:
    with old.connect(d,budget,connections,final=True) as db:final=op.execute(db,old.previous.fingerprints_sql(d),phase='final-fingerprint',label=0,reserve=0)[0][0]['snapshot']
    save(path/'final-snapshot.json',final);out['fingerprints_equal']=final==initial
    if not out['fingerprints_equal']:out.update(status='FAILED',exit=1)
   except BaseException as error:out.update(status='FAILED',exit=1,final_readback_error=type(error).__name__)
  if sampler is not None:
   try:
    sampler.boundary('end');stopped=sampler.receive('stopped');out['identity_equal']=ready is not None and stopped['identity']==ready['identity']
    if not out['identity_equal']:out.update(status='FAILED',exit=1)
   except BaseException as error:out.update(status='BLOCKED_PARTIAL',exit=1,sampler_final_error=type(error).__name__)
   finally:
    out['sampler_exit']=sampler.close();out['sampler_reaped']=True
    if out['sampler_exit']!=0:out.update(status='BLOCKED_PARTIAL',exit=1)
  op.close();records.close();connections.close()
  r=resource.getrusage(resource.RUSAGE_SELF);out.update(sql_attempted=budget.count,sql_completed=op.completed,sql_failed=op.failed,whole_wall_s=time.monotonic()-started,source_hashes=hashes(),client_highwater_rss_kib=r.ru_maxrss)
  save(path/'result.json',out)
 print('RESOURCE_PACKET',out['status'],'PGattempts',budget.count,flush=True)
 return out['exit']
if __name__=='__main__':
 os.umask(0o077)
 if sys.argv[1]=='declare':declare()
 else:sys.exit(execute())
