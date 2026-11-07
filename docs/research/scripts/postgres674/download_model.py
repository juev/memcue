"""Download only the pinned public model; bounded by declaration download budget."""
import hashlib,json,pathlib,subprocess,sys
MODEL='intfloat/multilingual-e5-small'
REV='614241f622f53c4eeff9890bdc4f31cfecc418b3'
if __name__=='__main__':
    cache=pathlib.Path(sys.argv[1]);cache.mkdir(mode=0o700,parents=True,exist_ok=True)
    evidence=[]
    for name in ['onnx/model.onnx','tokenizer.json','config.json','tokenizer_config.json']:
        p=cache/pathlib.Path(name).name
        if not p.exists():
            subprocess.run(['curl','--fail','--location','--silent','--show-error','--max-time','120',f'https://huggingface.co/{MODEL}/resolve/{REV}/{name}','-o',str(p)+'.partial'],check=True)
            pathlib.Path(str(p)+'.partial').rename(p)
        evidence.append(dict(file=name,sha256=hashlib.sha256(p.read_bytes()).hexdigest(),bytes=p.stat().st_size))
    assert sum(x['bytes'] for x in evidence)<800*1024**2
    (cache/'provenance.json').write_text(json.dumps(dict(model=MODEL,revision=REV,files=evidence),indent=2))
    print(json.dumps(evidence))
