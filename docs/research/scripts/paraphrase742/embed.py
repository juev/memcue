"""memcue #742: encode the frozen fixture and queries with both vector models, and time query encoding.

e5:  intfloat/multilingual-e5-small at the #682 revision, ONNX on this CPU: 'passage: ' / 'query: ' prefixes,
     right truncation at 512, attention-mask mean pooling, L2 normalisation, as in #682. Facts in one batch;
     each query alone, as a search encodes it, once cold and then PASSES-1 more times for timing.
bge: baai/bge-m3 on RouterAI (https://routerai.ru/api/v1/embeddings), no prefixes. Facts in batches of 32; each
     query in its own request, so its time is the full HTTP round trip from this machine, network included.

Usage (uv): uv run --with onnxruntime==1.23.2 --with tokenizers==0.22.1 --with numpy==2.3.4 \
  --with huggingface_hub==0.36.0 python -I embed.py FIXTURE QUERIES.json ROUTERAI_KEY_FILE OUT_DIR [PASSES]
Writes OUT_DIR/vectors.json.gz and OUT_DIR/encoding.json.
"""
import gzip
import hashlib
import json
import platform
import sys
import time
import urllib.request
from pathlib import Path

import numpy as np
import onnxruntime as ort
from huggingface_hub import hf_hub_download
from tokenizers import Tokenizer

fixture_path, queries_path, key_path, out_dir = sys.argv[1:5]
passes = int(sys.argv[5]) if len(sys.argv) > 5 else 6
out_dir = Path(out_dir)
facts = json.load(open(fixture_path))["facts"]
queries = json.load(open(queries_path))["queries"]

E5_REPO, E5_REV = "intfloat/multilingual-e5-small", "614241f622f53c4eeff9890bdc4f31cfecc418b3"


def sha256(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def e5():
    model_path = hf_hub_download(E5_REPO, "onnx/model.onnx", revision=E5_REV)
    tok_path = hf_hub_download(E5_REPO, "onnx/tokenizer.json", revision=E5_REV)
    tok = Tokenizer.from_file(tok_path)
    pad_id = tok.token_to_id("<pad>")
    tok.enable_truncation(512)
    tok.enable_padding(pad_id=pad_id, pad_token="<pad>")
    opts = ort.SessionOptions()
    opts.intra_op_num_threads, opts.inter_op_num_threads = 2, 1
    opts.execution_mode = ort.ExecutionMode.ORT_SEQUENTIAL
    sess = ort.InferenceSession(model_path, opts, providers=["CPUExecutionProvider"])
    names = [i.name for i in sess.get_inputs()]

    def encode(texts):
        enc = tok.encode_batch(texts)
        ids = np.array([e.ids for e in enc], dtype=np.int64)
        mask = np.array([e.attention_mask for e in enc], dtype=np.int64)
        feed = {"input_ids": ids, "attention_mask": mask}
        if "token_type_ids" in names:
            feed["token_type_ids"] = np.zeros_like(ids)
        hidden = sess.run(None, feed)[0]
        m = mask[..., None].astype(np.float32)
        v = (hidden * m).sum(1) / m.sum(1)
        return v / np.linalg.norm(v, axis=1, keepdims=True), int(ids.shape[1])

    fv, flen = encode([f"passage: {f['content']}" for f in facts])
    qv, times, qlen = {}, {}, 0
    for q in queries:
        ts = []
        for _ in range(passes):
            t = time.perf_counter()
            v, n = encode([f"query: {q['text']}"])
            ts.append((time.perf_counter() - t) * 1000)
        qv[q["id"]], times[q["id"]], qlen = v[0].tolist(), ts, max(qlen, n)
    meta = {"model": E5_REPO, "revision": E5_REV, "onnx_sha256": sha256(model_path),
            "tokenizer_sha256": sha256(tok_path), "pad_id": pad_id, "onnxruntime": ort.__version__,
            "inputs": names, "dims": int(fv.shape[1]), "fact_batch_tokens": flen, "max_query_tokens": qlen,
            "threads": "intra 2, inter 1, sequential", "cpu": platform.machine(), "python": platform.python_version()}
    return {f["id"]: fv[i].tolist() for i, f in enumerate(facts)}, qv, times, meta


def bge():
    key = Path(key_path).read_text().strip()

    def call(texts):
        body = json.dumps({"model": "baai/bge-m3", "input": texts}).encode()
        req = urllib.request.Request("https://routerai.ru/api/v1/embeddings", data=body,
                                     headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
        for attempt in range(3):
            t = time.perf_counter()
            try:
                with urllib.request.urlopen(req, timeout=120) as resp:
                    d = json.load(resp)
                ms = (time.perf_counter() - t) * 1000
                return [x["embedding"] for x in sorted(d["data"], key=lambda x: x["index"])], ms, attempt, d
            except Exception as e:  # retried, and the retry count is kept
                err = e
        raise err

    fv, tokens, served = {}, 0, set()
    for i in range(0, len(facts), 32):
        batch = facts[i:i + 32]
        vecs, _, _, d = call([f["content"] for f in batch])
        tokens += d["usage"]["prompt_tokens"]
        served.add(d.get("model"))
        for f, v in zip(batch, vecs):
            fv[f["id"]] = v
    qv, times, retries = {}, {}, 0
    for q in queries:
        ts = []
        for _ in range(passes):
            vecs, ms, attempt, d = call([q["text"]])
            ts.append(ms)
            retries += attempt
        qv[q["id"]], times[q["id"]] = vecs[0], ts
        served.add(d.get("model"))
    meta = {"model": "baai/bge-m3", "endpoint": "https://routerai.ru/api/v1/embeddings", "served_as": sorted(served),
            "dims": len(next(iter(fv.values()))), "fact_tokens": tokens, "query_retries": retries}
    return fv, qv, times, meta


def rounded(vs):
    # Six decimals keep every cosine within 1e-5 and halve the published file; the ranking reads these values.
    return {k: [round(float(x), 6) for x in v] for k, v in vs.items()}


models, encoding = {}, {"passes": passes, "date": time.strftime("%Y-%m-%d")}
for name, fn in (("e5", e5), ("bge", bge)):
    fv, qv, times, meta = fn()
    models[name] = {"facts": rounded(fv), "queries": rounded(qv)}
    encoding[name] = {"meta": meta, "query_ms": times}
    print(name, meta)
with gzip.GzipFile(out_dir / "vectors.json.gz", "wb", mtime=0) as w:
    w.write(json.dumps({"models": models}).encode())
(out_dir / "encoding.json").write_text(json.dumps(encoding, indent=1) + "\n")
