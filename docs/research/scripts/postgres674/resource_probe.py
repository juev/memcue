"""Bounded read-only cgroup sampler, parser and owned SSH cleanup."""
import json
import queue
import signal
import subprocess
import threading
import time
from ann_shape_exact import Journal


def parse_sample(row):
    cpu=row.get('cpu',{}).get('usage_usec')
    memory=row.get('memory_current')
    if not isinstance(cpu,int) or not isinstance(memory,int) or cpu<0 or memory<0:
        raise ValueError('required cgroup counters missing or invalid')
    return dict(cpu_seconds=cpu/1000000,memory_current_bytes=memory,
                anon_bytes=row.get('memory_stat',{}).get('anon'),file_bytes=row.get('memory_stat',{}).get('file'),
                swap_bytes=row.get('memory_swap_current'),memory_max_bytes=row.get('memory_max'),
                memory_unlimited=row.get('memory_unlimited'),events=row.get('memory_events',{}))


def delta(before,after):
    a=parse_sample(before);b=parse_sample(after)
    if b['cpu_seconds']<a['cpu_seconds'] or after['remote_mono']<=before['remote_mono']:
        raise ValueError('cumulative counter or clock reset')
    return dict(wall_s=after['remote_mono']-before['remote_mono'],cpu_seconds=b['cpu_seconds']-a['cpu_seconds'],
                memory_current_before_bytes=a['memory_current_bytes'],memory_current_after_bytes=b['memory_current_bytes'],
                memory_events_delta={k:(b['events'][k]-v if k in b['events'] else None) for k,v in a['events'].items()})


def remote_loop(sample,identity,stdin,emit,clock=time.monotonic,wait=None,lifetime=90,interval=.5):
    """Bound input waits; sample/identity callbacks and output may still block."""
    import os,select
    wait=wait or (lambda stream,timeout:bool(select.select([stream],[],[],timeout)[0]))
    start=clock();phase='baseline'
    pending=b'';eof=False;fd=None;blocking=None
    try:fd=stdin.fileno()
    except (AttributeError,OSError):pass  # In-memory test streams cannot block.
    if fd is not None:
        blocking=os.get_blocking(fd);os.set_blocking(fd,False)
    try:
        emit(dict(kind='ready',identity=identity(),phase=phase,**sample()))
        while clock()-start<lifetime:
            if fd is not None and (b'\n' in pending or eof):
                line,separator,pending=pending.partition(b'\n')
                if not separator:pending=b''
                line+=separator  # Preserve readline's blank-line parse error.
                line=line.decode(getattr(stdin,'encoding',None) or 'utf-8')
            elif wait(stdin,min(interval,max(0,lifetime-(clock()-start)))):
                if fd is not None:
                    try:chunk=os.read(fd,4096)
                    except BlockingIOError:continue
                    if chunk:pending+=chunk
                    else:eof=True
                    continue
                line=stdin.readline()
            else:
                emit(dict(kind='sample',phase=phase,**sample()))
                continue
            if line:
                value=json.loads(line)
                phase=value['phase'];seq=value['seq']
                if phase not in ('baseline','reference','ann-40','ann-100','end'):raise ValueError('phase rejected')
                emit(dict(kind='boundary',seq=seq,phase=phase,**sample()))
                if phase=='end':break
            else:break
        emit(dict(kind='stopped',identity=identity()))
    except BrokenPipeError:return
    finally:
        if fd is not None:os.set_blocking(fd,blocking)


def remote_source():
    import inspect
    return 'import time\n'+inspect.getsource(remote_loop)+'''\nimport json,pathlib,subprocess,sys,time
cid,name,image=sys.argv[1:]
fmt='{"id":{{json .Id}},"name":{{json .Name}},"status":{{json .State.Status}},"pid":{{json .State.Pid}},"started":{{json .State.StartedAt}},"restarts":{{json .RestartCount}}}'
def identity():
 r=subprocess.run(['docker','inspect','--format',fmt,cid],capture_output=True,text=True,timeout=4)
 if r.returncode:raise RuntimeError('minimal inspect failed')
 v=json.loads(r.stdout)
 if not (v['id'].startswith(cid) and v['name']=='/'+name and v['status']=='running'):raise ValueError('target identity rejected')
 return v
initial=identity()
row=next(x for x in pathlib.Path('/proc/'+str(initial['pid'])+'/cgroup').read_text().splitlines() if x.startswith('0::'))
rel=row[3:]
if not rel.startswith('/') or '..' in pathlib.PurePosixPath(rel).parts:raise ValueError('cgroup path rejected')
base=pathlib.Path('/sys/fs/cgroup'+rel)
def read(name):
 try:return (base/name).read_text().strip()
 except OSError:return None
def pairs(name):
 value=read(name)
 return {k:int(v) for k,v in (s.split() for s in value.splitlines())} if value is not None else {}
def number(name):
 value=read(name)
 return int(value) if value is not None and value!='max' else None
def sample():
 maximum=read('memory.max')
 return dict(remote_mono=time.monotonic(),cpu=pairs('cpu.stat'),memory_current=number('memory.current'),
 memory_stat=pairs('memory.stat'),memory_swap_current=number('memory.swap.current'),memory_max=number('memory.max'),
 memory_unlimited=maximum=='max' if maximum is not None else None,memory_events=pairs('memory.events'),cpu_max=read('cpu.max'))
def emit(value):print(json.dumps(value),flush=True)
remote_loop(sample,identity,sys.stdin,emit)
'''


def stop_owned(process,timeout=2):
    """Reap exactly the process group created by the caller; no fleet cleanup."""
    import os
    try:
        if process.stdin and not process.stdin.closed:process.stdin.close()
    except (OSError,ValueError):pass
    try:process.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        os.killpg(process.pid,signal.SIGTERM)
        try:process.wait(timeout=timeout)
        except subprocess.TimeoutExpired:
            os.killpg(process.pid,signal.SIGKILL);process.wait(timeout=timeout)
    return process.returncode


class Sampler:
    def __init__(self,argv,path):
        self.argv=argv;self.path=path;self.responses=queue.Queue();self.rows=[];self.seq=0
        self.raw=Journal(path/'samples.jsonl');self.err=(path/'ssh-stderr.txt').open('x')
        self.process=subprocess.Popen(argv,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=self.err,text=True,start_new_session=True)
        self.thread=threading.Thread(target=self._read,daemon=True);self.thread.start()
    def _read(self):
        try:
            for line in self.process.stdout:
                row=json.loads(line);row['received_mono']=time.monotonic();self.rows.append(row);self.raw.write(row)
                if row['kind'] in ('ready','boundary','stopped'):self.responses.put(row)
        except BaseException as error:self.responses.put(error)
        finally:self.responses.put(EOFError('sampler stream closed'))
    def receive(self,kind,timeout=6):
        row=self.responses.get(timeout=timeout)
        if isinstance(row,BaseException):raise row
        if row['kind']!=kind:raise ValueError('sampler response category mismatch')
        if kind in ('ready','boundary'):parse_sample(row)
        return row
    def boundary(self,phase):
        self.seq+=1
        self.process.stdin.write(json.dumps(dict(phase=phase,seq=self.seq))+'\n');self.process.stdin.flush()
        row=self.receive('boundary')
        if row['seq']!=self.seq or row['phase']!=phase:raise ValueError('sampler boundary mismatch')
        return row
    def close(self):
        code=stop_owned(self.process);self.thread.join(timeout=2)
        self.process.stdout.close();self.err.close();self.raw.close()
        if self.thread.is_alive():raise RuntimeError('sampler reader not reaped')
        return code


def install_guard(started):
    """Stop work by90s, leaving30s for readback and owned process cleanup."""
    remaining=started+90-time.monotonic()
    if remaining<=0:raise TimeoutError('resource work deadline exceeded')
    def stop(signum,frame):
        clear_guard()
        raise TimeoutError('resource work interrupted')
    signal.signal(signal.SIGTERM,stop);signal.signal(signal.SIGALRM,stop)
    signal.setitimer(signal.ITIMER_REAL,remaining)


def clear_guard():
    signal.setitimer(signal.ITIMER_REAL,0)
    signal.signal(signal.SIGTERM,signal.SIG_IGN)
