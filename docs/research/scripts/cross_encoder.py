"""Score the judge candidates with a local cross-encoder through ONNX Runtime on the CPU, and time it (#317).

  python3 -I cross_encoder.py MODEL_DIR ONNX_FILE candidates.jsonl OUT.jsonl

MODEL_DIR holds tokenizer.json; ONNX_FILE is the model inside it (onnx/model.onnx, onnx/model_qint8_arm64.onnx).
OUT.jsonl is a judge_ab.py reader: per prompt the candidates, the sigmoid of the model's logit as the score, and the
seconds the prompt's pairs took in one batch. The first line printed is the cold start: session creation and tokenizer
load, which a fresh hook process pays every time. THREADS sets intra-op threads (default: ONNX Runtime's choice).
Needs onnxruntime, tokenizers and numpy.
"""
import json
import os
import statistics
import sys
import time
from pathlib import Path

import numpy as np
import onnxruntime as ort
from tokenizers import Tokenizer

MAX_LEN = 512

model_dir, onnx_file, cand_path, out_path = sys.argv[1:5]
t0 = time.perf_counter()
opts = ort.SessionOptions()
if os.environ.get("THREADS"):
    opts.intra_op_num_threads = int(os.environ["THREADS"])
sess = ort.InferenceSession(str(Path(model_dir) / onnx_file), opts, providers=["CPUExecutionProvider"])
tok = Tokenizer.from_file(str(Path(model_dir) / "tokenizer.json"))
tok.enable_truncation(MAX_LEN)
tok.enable_padding()
cold = time.perf_counter() - t0
inputs = {i.name for i in sess.get_inputs()}
print(f"cold start {cold * 1000:.0f} ms, inputs {sorted(inputs)}")


def score(prompt, facts):
    enc = tok.encode_batch([(prompt, f) for f in facts])
    feed = {"input_ids": np.array([e.ids for e in enc], dtype=np.int64),
            "attention_mask": np.array([e.attention_mask for e in enc], dtype=np.int64)}
    if "token_type_ids" in inputs:
        feed["token_type_ids"] = np.array([e.type_ids for e in enc], dtype=np.int64)
    logits = sess.run(None, feed)[0].reshape(-1)
    return 1 / (1 + np.exp(-logits))


times = []
with open(out_path, "w") as w:
    for r in map(json.loads, open(cand_path)):
        cands = r["candidates"] or []
        if len(cands) < 2:
            continue
        t = time.perf_counter()
        s = score(r["text"], [c["content"] for c in cands])
        times.append(time.perf_counter() - t)
        w.write(json.dumps({"id": r["id"], "facts": [c["id"] for c in cands],
                            "provisional": [c["provisional"] for c in cands],
                            "scores": [round(float(x), 4) for x in s], "seconds": times[-1]}) + "\n")
times.sort()
print(f"prompts {len(times)}, pairs per prompt up to 20: median {statistics.median(times) * 1000:.0f} ms, "
      f"p90 {times[int(len(times) * 0.9)] * 1000:.0f} ms, max {times[-1] * 1000:.0f} ms")
