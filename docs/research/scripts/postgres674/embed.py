"""Genuine pinned E5 ONNX embeddings, not hand-assigned vectors."""
import hashlib,json,time
from pathlib import Path
TAG='e5-small-614241f-f32-mean-l2-384'
def content_hash(s): return hashlib.sha256(s.encode()).hexdigest()
class Embedder:
    def __init__(self,cache):
        import onnxruntime as ort
        from tokenizers import Tokenizer
        cache=Path(cache)
        self.provenance=json.loads((cache/'provenance.json').read_text())
        for f in self.provenance['files']:
            p=cache/Path(f['file']).name
            assert hashlib.sha256(p.read_bytes()).hexdigest()==f['sha256']
        self.tokenizer=Tokenizer.from_file(str(cache/'tokenizer.json'))
        self.tokenizer.enable_padding(pad_id=1,pad_token='<pad>')
        self.tokenizer.enable_truncation(max_length=512)
        options=ort.SessionOptions(); options.intra_op_num_threads=2; options.inter_op_num_threads=1
        self.session=ort.InferenceSession(str(cache/'model.onnx'),sess_options=options,providers=['CPUExecutionProvider'])
    def encode(self,texts,prefix):
        import numpy as np
        start=time.perf_counter()
        toks=self.tokenizer.encode_batch([prefix+s for s in texts])
        ids=np.asarray([t.ids for t in toks],dtype=np.int64)
        mask=np.asarray([t.attention_mask for t in toks],dtype=np.int64)
        inputs={'input_ids':ids,'attention_mask':mask}
        if any(x.name=='token_type_ids' for x in self.session.get_inputs()): inputs['token_type_ids']=np.zeros_like(ids)
        outputs=self.session.run(None,inputs)[0]
        mean=(outputs*mask[:,:,None]).sum(1)/mask.sum(1)[:,None]
        mean=mean/np.linalg.norm(mean,axis=1,keepdims=True)
        assert mean.shape==(len(texts),384) and np.isfinite(mean).all()
        return mean.astype(np.float32).tolist(),(time.perf_counter()-start)*1000

def literal(v):
    assert len(v)==384
    return '['+','.join(format(x,'.9g') for x in v)+']'
