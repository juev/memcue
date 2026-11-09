"""Finite lifecycle controls, not database durability evidence."""
import importlib.util
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time
import unittest

ROOT=Path(__file__).resolve().parents[2]

if os.environ.get('MEMCUE_HERMES_DISTRIBUTION'):
 sys.path.insert(0,os.environ['MEMCUE_HERMES_DISTRIBUTION'])
def load(name,path):
 s=importlib.util.spec_from_file_location(name,path);m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
class OwnedProcess(unittest.TestCase):
 def test_timeout_stops_owned_descendant_and_preserves_unrelated_process(self):
  hermes=load('owned_hermes',ROOT/'plugins/hermes/memcue/__init__.py')
  codex=load('owned_codex',ROOT/'plugins/codex/scripts/memcue.py')
  with tempfile.TemporaryDirectory(prefix="memcue-owned-") as d:
   d=Path(d)
   sentinel=subprocess.Popen([sys.executable,'-c','import time;time.sleep(12)'])
   try:
    for name in ('hermes','codex'):
     with self.subTest(caller=name):
      pid=d/(name+'.pid'); ran=d/(name+'.ran'); exe=d/name
      exe.write_text('#!'+sys.executable+'\nimport os,time,pathlib\nif os.fork()==0:\n pathlib.Path('+repr(str(pid))+').write_text(str(os.getpid()));time.sleep(2.1);pathlib.Path('+repr(str(ran))+').write_text("survived");os._exit(0)\ntime.sleep(2.0)\n')
      exe.chmod(0o700)
      start=time.monotonic()
      if name=='hermes':
       p=hermes.MemcueMemoryProvider({'binary':str(exe),'dream':False});ok,answer=p._run(['add','--','synthetic'],timeout=1.5)
      else:
       try:codex.call({'binary':str(exe),'facts':str(d/'facts'),'state_dir':str(d/'state'),'host':'synthetic'},['add','--','synthetic'],str(d),timeout=1.5);ok,answer=True,''
       except codex.AdapterError as e:ok,answer=False,str(e)
      self.assertFalse(ok);self.assertIn('UNKNOWN OUTCOME',answer)
      self.assertLess(time.monotonic()-start,1.85)
      self.assertTrue(pid.exists(),'actual descendant must start')
      proc=Path('/proc')/pid.read_text()/'stat'
      running=proc.exists() and proc.read_text().split(') ',1)[1][0] not in 'ZX'
      self.assertFalse(running,'invocation descendant still running at return')
      time.sleep(2.2)
      self.assertFalse(ran.exists(),'owned child reached finite post-timeout action')
      self.assertIsNone(sentinel.poll(),'unrelated sentinel was signalled')
   finally:sentinel.terminate();sentinel.wait()
 def test_capture_preserves_complete_finite_read_output(self):
  h=load('capture_hermes',ROOT/'plugins/hermes/memcue/__init__.py')
  with tempfile.TemporaryDirectory(prefix='memcue-capture-') as d:
   exe=Path(d)/'finite-read';size=4*1024*1024+257
   exe.write_text('#!'+sys.executable+'\nimport sys\nsys.stdout.write("x"*'+str(size)+')\n');exe.chmod(0o700)
   p=h.MemcueMemoryProvider({'binary':str(exe),'dream':False})
   ok,out=p._run(['history','synthetic'],timeout=2)
   self.assertTrue(ok);self.assertEqual(len(out),size,'transport must not silently truncate an accepted finite read')
 def test_post_attempt_file_read_failure_is_uncertain(self):
  from unittest.mock import patch
  h=load('file_hermes',ROOT/'plugins/hermes/memcue/__init__.py');c=load('file_codex',ROOT/'plugins/codex/scripts/memcue.py')
  original=tempfile.TemporaryFile
  class FailedRead:
   def __init__(self,*a,**kw):self.file=original(*a,**kw)
   def __getattr__(self,n):return getattr(self.file,n)
   def __enter__(self):return self
   def __exit__(self,*a):self.file.close()
   def read(self,*a):raise OSError('synthetic response read fault')
  with tempfile.TemporaryDirectory(prefix='memcue-file-fault-') as d:
   d=Path(d)
   for name,m in [('hermes',h),('codex',c)]:
    with self.subTest(caller=name):
     mark=d/(name+'.attempted');exe=d/name
     exe.write_text('#!'+sys.executable+'\nfrom pathlib import Path\nPath('+repr(str(mark))+').write_text("attempted")\n');exe.chmod(0o700)
     with patch.object(m.tempfile,'TemporaryFile',FailedRead):
      if name=='hermes':ok,answer=h.MemcueMemoryProvider({'binary':str(exe),'dream':False})._run(['add','--','synthetic'],timeout=2)
      else:
       try:c.call({'binary':str(exe),'facts':str(d/'facts'),'state_dir':str(d/'state'),'host':'synthetic'},['add','--','synthetic'],str(d),timeout=2);ok,answer=True,''
       except Exception as e:ok,answer=False,str(e)
     self.assertTrue(mark.exists(),'actual executable must have started')
     self.assertFalse(ok);self.assertIn('UNKNOWN OUTCOME',answer)
 def test_group_of_only_the_unreaped_leader_is_not_a_transport_failure(self):
  # macOS answers killpg with EPERM, not ESRCH, when only the unreaped leader
  # of the owned group remains; a finished read must still succeed.
  from unittest.mock import patch
  h=load('eperm_hermes',ROOT/'plugins/hermes/memcue/__init__.py');c=load('eperm_codex',ROOT/'plugins/codex/scripts/memcue.py')
  with tempfile.TemporaryDirectory(prefix='memcue-eperm-') as d:
   d=Path(d);exe=d/'finite-read'
   exe.write_text('#!'+sys.executable+'\nimport sys\nsys.stdout.write("{}")\n');exe.chmod(0o700)
   def eperm(pgid,sig):raise PermissionError(1,'Operation not permitted')
   for name in ('hermes','codex'):
    with self.subTest(caller=name),patch('os.killpg',eperm):
     if name=='hermes':
      ok,out=h.MemcueMemoryProvider({'binary':str(exe),'dream':False})._run(['facts'],timeout=2)
     else:
      try:out=c.call({'binary':str(exe),'facts':str(d/'facts'),'state_dir':str(d/'state'),'host':'synthetic'},['facts'],str(d),timeout=2);ok=True
      except c.AdapterError as e:ok,out=False,str(e)
     self.assertTrue(ok,out);self.assertEqual(out.strip(),'{}')
if __name__=='__main__':unittest.main()
