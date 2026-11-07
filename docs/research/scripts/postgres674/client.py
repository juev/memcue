"""One measured CLI invocation. Reads a private request path; prints JSON only."""
import http.client,json,os,sys,time
from pathlib import Path

def main():
    config=json.loads(Path(sys.argv[1]).read_text());req=json.loads(Path(sys.argv[2]).read_text());start=time.perf_counter()
    if sys.argv[3]=='http':
        conn=http.client.HTTPConnection('127.0.0.1',config['port'],timeout=4)
        conn.request('POST','/v1/search',json.dumps(req),{'Authorization':'Bearer '+config['token'],'Content-Type':'application/json'})
        response=conn.getresponse();body=json.loads(response.read());conn.close()
        if response.status!=200: raise RuntimeError(body)
    else:
        from backend import connect,search
        from normalize import Normalizer
        norm=Normalizer(config['bridge']).query(req['query'])
        with connect(config['dsn']) as db: body=search(db,norm,req['query'],'pg-lexical',limit=req['query']['N'])
    body['client_ms']=(time.perf_counter()-start)*1000
    print(json.dumps(body))
if __name__=='__main__': main()
