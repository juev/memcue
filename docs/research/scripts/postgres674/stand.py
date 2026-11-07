"""Own one disposable PostgreSQL process and always stop it before returning."""
import contextlib,json,os,secrets,signal,socket,subprocess,time
from pathlib import Path
import psycopg

class Stand:
    def __init__(self,root,out):
        self.root=Path(root).resolve();self.out=Path(out).resolve();self.data=self.out/'pgdata'
        self.bin=self.root/'usr/lib/postgresql/17/bin'
        self.env=dict(os.environ,LD_LIBRARY_PATH=str(self.root/'usr/lib/x86_64-linux-gnu'))
        self.password=secrets.token_urlsafe(32);self.proc=None
        self.lifecycle=[]
        with socket.socket() as sock: sock.bind(('127.0.0.1',0));self.port=sock.getsockname()[1]
    def command(self,name,*args,**kw): return subprocess.run([str(self.bin/name),*map(str,args)],env=self.env,check=True,**kw)
    def dsn(self,db='research'): return f'host=127.0.0.1 port={self.port} dbname={db} user=research password={self.password} connect_timeout=2'
    def init(self):
        password=self.out/'password';password.write_text(self.password);password.chmod(0o600)
        with (self.out/'initdb.log').open('w') as log:
            self.command('initdb','-D',self.data,'-U','research','--auth=scram-sha-256','--pwfile',password,'--no-locale','--encoding=UTF8',stdout=log,stderr=subprocess.STDOUT)
        password.unlink()
        self.lifecycle.append('initdb completed; no system service registration')
    def start(self):
        self.log=(self.out/'postgres.log').open('a')
        self.proc=subprocess.Popen([str(self.bin/'postgres'),'-D',str(self.data),'-h','127.0.0.1','-p',str(self.port),'-k','',
            '-c','shared_buffers=32MB','-c','max_connections=10','-c','work_mem=4MB','-c','jit=off',
            '-c','max_parallel_workers=0','-c','max_worker_processes=2','-c','autovacuum_max_workers=1'],env=self.env,stdout=self.log,stderr=subprocess.STDOUT)
        for _ in range(100):
            if self.proc.poll() is not None: raise RuntimeError('Postgres startup failed; see private postgres.log')
            try:
                with psycopg.connect(self.dsn('postgres')): break
            except psycopg.OperationalError: time.sleep(.05)
        else: raise RuntimeError('Postgres readiness deadline exceeded')
        self.lifecycle.append('started rootless, loopback SCRAM, bounded settings')
    def stop(self):
        if self.proc is not None:
            self.proc.send_signal(signal.SIGINT)
            try: self.proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                self.proc.kill();self.proc.wait();self.lifecycle.append('forced process cleanup')
            self.log.close();self.lifecycle.append('stopped owned process, exit '+str(self.proc.returncode));self.proc=None
            (self.out/'lifecycle.json').write_text(json.dumps(self.lifecycle,indent=2))
    def dump_restore(self,source,target):
        dump=self.out/'backup.dump'
        env=dict(self.env,PGHOST='127.0.0.1',PGPORT=str(self.port),PGUSER='research',PGPASSWORD=self.password)
        for command in [[str(self.bin/'pg_dump'),'-Fc','-d',source,'-f',str(dump)],
                        [str(self.bin/'createdb'),target],
                        [str(self.bin/'pg_restore'),'--exit-on-error','-d',target,str(dump)]]:
            subprocess.run(command,env=env,check=True,capture_output=True)
        self.lifecycle.append('pg_dump custom archive and pg_restore --exit-on-error completed')
