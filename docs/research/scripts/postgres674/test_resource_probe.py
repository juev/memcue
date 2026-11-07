import io
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from resource_probe import parse_sample,delta,remote_loop,stop_owned,Sampler


def row(cpu=2000000):
    return dict(cpu={'usage_usec':cpu},memory_current=123,memory_stat={'anon':40,'file':50},memory_events={'oom':7},remote_mono=1.)


class ResourceTests(unittest.TestCase):
    def test_real_parser_units_missing_fields_and_counter_delta(self):
        self.assertEqual(parse_sample(row())['cpu_seconds'],2.)
        self.assertIsNone(parse_sample(row())['swap_bytes'])
        with self.assertRaises(ValueError):parse_sample({'memory_current':1})
        a=row();b=row(3500000);b['remote_mono']=3.;b['memory_events']['oom']=8
        self.assertEqual(delta(a,b)['cpu_seconds'],1.5)
        self.assertEqual(delta(a,b)['memory_events_delta']['oom'],1)
        with self.assertRaises(ValueError):delta(b,a)

    def test_remote_loop_eof_broken_pipe_and_independent_lifetime(self):
        out=[];remote_loop(row,lambda:{'synthetic':True},io.StringIO(''),out.append,wait=lambda *a:True)
        self.assertEqual([x['kind'] for x in out],['ready','stopped'])
        def broken(value):raise BrokenPipeError()
        remote_loop(row,lambda:{},io.StringIO(''),broken,wait=lambda *a:True)
        now=[0.]
        def wait(stream,seconds):now[0]+=seconds;return False
        out=[];remote_loop(row,lambda:{},io.StringIO(''),out.append,clock=lambda:now[0],wait=wait,lifetime=1,interval=.25)
        self.assertLessEqual(now[0],1);self.assertEqual(out[-1]['kind'],'stopped')

    def test_actual_process_cleanup_and_partial_stream_eof(self):
        with tempfile.TemporaryDirectory(dir=os.environ['TMPDIR']) as tmp:
            p=Path(tmp)
            script="import json; print(json.dumps({'kind':'ready','cpu':{'usage_usec':0},'memory_current':1}),flush=True)"
            sampler=Sampler([sys.executable,'-c',script],p)
            self.assertEqual(sampler.receive('ready')['memory_current'],1)
            with self.assertRaises(EOFError):sampler.receive('boundary')
            self.assertEqual(sampler.close(),0)
            self.assertTrue((p/'samples.jsonl').read_text())
            child=subprocess.Popen([sys.executable,'-c','import time;time.sleep(30)'],stdin=subprocess.PIPE,start_new_session=True)
            stop_owned(child,timeout=.05);self.assertIsNotNone(child.poll())

if __name__=='__main__':unittest.main()


class GuardTests(unittest.TestCase):
    def test_guard_arms_term_and_actual_deadline_runs_finally(self):
        import signal,time
        from resource_probe import install_guard
        old=signal.getsignal(signal.SIGTERM);alarm=signal.getsignal(signal.SIGALRM)
        try:
            install_guard(time.monotonic())
            self.assertTrue(callable(signal.getsignal(signal.SIGTERM)))
        finally:
            signal.setitimer(signal.ITIMER_REAL,0);signal.signal(signal.SIGTERM,old);signal.signal(signal.SIGALRM,alarm)
        script="from resource_probe import install_guard;import time;install_guard(time.monotonic()-89.9)\ntry:time.sleep(30)\nfinally:print('cleanup-ran',flush=True)"
        result=subprocess.run([sys.executable,'-c',script],capture_output=True,text=True,timeout=3)
        self.assertNotEqual(result.returncode,0);self.assertIn('cleanup-ran',result.stdout)


class PartialInputTests(unittest.TestCase):
    def remote(self, prefix='', lifetime=5):
        from contextlib import contextmanager
        @contextmanager
        def owned():
            read_fd,write_fd=os.pipe()
            if prefix:os.write(write_fd,prefix.encode())
            child=None
            try:
                script=("import sys,time,json;from resource_probe import remote_loop\n"
                        "def sample():return {'cpu':{'usage_usec':0},'memory_current':1,'remote_mono':time.monotonic()}\n"
                        "def emit(value):print(json.dumps(value),flush=True)\n"
                        "remote_loop(sample,lambda:{'synthetic':True},sys.stdin,emit,lifetime="+str(lifetime)+",interval=.05)\n")
                child=subprocess.Popen([sys.executable,'-u','-c',script],stdin=read_fd,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,start_new_session=True)
                os.close(read_fd);read_fd=None
                child.stdin=os.fdopen(write_fd,'w',buffering=1);write_fd=None
                import select
                self.assertTrue(select.select([child.stdout],[],[],8)[0],'owned child did not become ready')
                self.assertEqual(__import__('json').loads(child.stdout.readline())['kind'],'ready')
                yield child
            finally:
                if read_fd is not None:os.close(read_fd)
                if write_fd is not None:os.close(write_fd)
                if child is not None:
                    stop_owned(child,timeout=.2)
                    child.stdout.close();child.stderr.close()
                    self.assertIsNotNone(child.poll(),'owned child was not reaped')
        return owned()

    def finish(self,child,timeout=8):
        try:child.wait(timeout=timeout)
        except subprocess.TimeoutExpired:self.fail('sampler did not finish within supervisory timeout while input stayed open')
        self.assertEqual(child.returncode,0)
        return [__import__('json').loads(line) for line in child.stdout.read().splitlines()]

    def test_partial_line_deadline_with_writer_held_open(self):
        # Input exists before child startup, so no sub-second parent scheduling
        # assumption is needed. The one-second sampler deadline has an eight-
        # second supervisor; cleanup closes the writer only after the assertion.
        with self.remote(prefix='{',lifetime=1) as child:
            self.assertFalse(child.stdin.closed)
            rows=self.finish(child)
            self.assertFalse(child.stdin.closed)
            self.assertEqual(rows[-1]['kind'],'stopped')
            self.assertFalse(any(r['kind']=='boundary' for r in rows))

    def test_split_then_complete_and_pipelined_end(self):
        with self.remote(prefix='{"phase":"ref') as child:
            child.stdin.write('erence","seq":7}\n{"phase":"end","seq":8}\n');child.stdin.flush()
            rows=self.finish(child)
            self.assertEqual([(r['phase'],r['seq']) for r in rows if r['kind']=='boundary'],[('reference',7),('end',8)])
            self.assertEqual(rows[-1]['kind'],'stopped')

    def test_eof_empty_and_unterminated_complete_command(self):
        for prefix in ('','{"phase":"baseline","seq":9}'):
            with self.subTest(empty=not prefix),self.remote(prefix=prefix) as child:
                child.stdin.close();rows=self.finish(child)
                self.assertEqual(rows[-1]['kind'],'stopped')
                self.assertEqual([r['seq'] for r in rows if r['kind']=='boundary'],[9] if prefix else [])

    def test_blank_line_remains_a_parse_error(self):
        with self.remote(prefix='\n') as child:
            try:child.wait(timeout=8)
            except subprocess.TimeoutExpired:self.fail('blank-line control did not finish')
            self.assertNotEqual(child.returncode,0)
