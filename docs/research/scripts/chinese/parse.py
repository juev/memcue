"""parse.py OUT.jsonl IN.json... : JSON lines from claude -p envelopes; prints cost, time and line count."""
import json,sys
cost=0;ms=0;n=0;out=open(sys.argv[1],'w')
for f in sys.argv[2:]:
    d=json.load(open(f)); cost+=d['total_cost_usd']; ms+=d.get('duration_api_ms',0)
    for l in d['result'].splitlines():
        l=l.strip()
        if l.startswith('{'):
            try: json.loads(l); out.write(l+'\n'); n+=1
            except Exception as e: print('bad',f,l[:80])
print(sys.argv[1],'lines',n,'cost',round(cost,4),'api_s',round(ms/1000,1))
