"""Managed, loopback-only prototype. Never launch as an unattended service."""
import concurrent.futures,contextlib,hmac,json,socket,threading,time
from http.server import BaseHTTPRequestHandler,HTTPServer
from psycopg_pool import ConnectionPool
from psycopg.rows import dict_row
from backend import ContractError,search,mutate

class LimitedServer(HTTPServer):
    def __init__(self,*args):
        super().__init__(*args);self.executor=concurrent.futures.ThreadPoolExecutor(max_workers=2)
    def process_request(self,request,address): self.executor.submit(self.handle,request,address)
    def handle(self,request,address):
        request.settimeout(4)
        request.setsockopt(socket.IPPROTO_TCP,socket.TCP_NODELAY,1)
        try: self.finish_request(request,address)
        finally: self.shutdown_request(request)
    def server_close(self):
        super().server_close();self.executor.shutdown(wait=True)

@contextlib.contextmanager
def service(dsn,token,normalizer):
    allowed=['repo:demo','host:client-a','global','shelf:research']
    pool=ConnectionPool(dsn,min_size=1,max_size=2,num_workers=1,timeout=3,kwargs=dict(autocommit=True,row_factory=dict_row))
    class Handler(BaseHTTPRequestHandler):
        protocol_version='HTTP/1.1'
        def log_message(self,*args): pass
        def do_POST(self):
            status=200
            try:
                if not hmac.compare_digest(self.headers.get('Authorization',''),'Bearer '+token): raise ContractError('unauthorized',401)
                n=int(self.headers.get('Content-Length','0'))
                if not 0<n<=65536: raise ContractError('payload_limit',413)
                req=json.loads(self.rfile.read(n));deadline_ms=req.get('deadline_ms',3000)
                if not isinstance(deadline_ms,int) or not 1<=deadline_ms<=3000: raise ContractError('invalid_deadline',400)
                started=time.perf_counter()
                if req.get('caller')!={'project':['demo'],'host':'client-a'}: raise ContractError('forbidden_caller',403)
                with pool.connection() as db:
                    with db.transaction():
                        db.execute("SELECT set_config('statement_timeout',%s,true)",(str(deadline_ms),))
                        if self.path=='/v1/search':
                            q=req['query'];norm=normalizer.query(q)
                            if time.perf_counter()-started>deadline_ms/1000: raise ContractError('deadline',504)
                            if q.get('Project')!=['demo'] or q.get('Host')!='client-a': raise ContractError('forbidden_caller',403)
                            if not isinstance(q.get('N'),int) or not 1<=q['N']<=100: raise ContractError('invalid_limit',400)
                            # Lexical-only transport experiment. Embedding service is a pending protocol extension.
                            if req.get('arm','pg-lexical')!='pg-lexical': raise ContractError('embedding_route_unimplemented',501)
                            reply=search(db,norm,q,'pg-lexical',limit=q['N'],authorized=allowed)
                        elif self.path in ['/v1/get','/v1/history']:
                            order=' ORDER BY version' if self.path.endswith('history') else ' ORDER BY version DESC LIMIT 1'
                            rows=db.execute('SELECT * FROM facts WHERE id=%s AND lower(scope)=ANY(%s)'+order,(req['id'],allowed)).fetchall()
                            if not rows: raise ContractError('not_found',404)
                            reply=dict(facts=rows)
                        elif self.path=='/v1/mutate':
                            from datetime import datetime
                            stamp=datetime.fromisoformat(req['now'].replace('Z','+00:00'))
                            if stamp.utcoffset().total_seconds()!=0: raise ContractError('utc_timestamp_required',400)
                            reply=mutate(db,'fixture-client',self.headers.get('Idempotency-Key',''),req,normalizer.stems,allowed)
                        else: raise ContractError('unsupported_endpoint',404)
            except ContractError as e: status=e.status;reply=dict(error=dict(code=e.code,retryable=e.status in [503,504]))
            except (ValueError,KeyError,TypeError,AttributeError): status=400;reply=dict(error=dict(code='invalid_request',retryable=False))
            except Exception as e:
                import psycopg
                if isinstance(e,psycopg.errors.QueryCanceled): status=504;reply=dict(error=dict(code='deadline',retryable=True))
                else: status=503;reply=dict(error=dict(code='backend_unavailable',retryable=True))
            body=json.dumps(reply,ensure_ascii=False).encode()
            self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(body)));self.end_headers()
            try: self.wfile.write(body)
            except (BrokenPipeError,ConnectionResetError): pass
    server=LimitedServer(('127.0.0.1',0),Handler)
    thread=threading.Thread(target=server.serve_forever,name='research-api');thread.start()
    try:
        pool.wait(timeout=5)
        yield server.server_address[1]
    finally:
        server.shutdown();server.server_close();thread.join();pool.close()
