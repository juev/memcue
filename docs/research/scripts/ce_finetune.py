"""Fine-tune a cross-encoder on Jev's probabilities over prompts outside the gold set, then score the gold candidates
(#317, with the teacher labels of #320).

  python3 -I ce_finetune.py BASE_MODEL_DIR pool_candidates.jsonl pool_jev.jsonl gold_candidates.jsonl OUT.jsonl

The target of every pair is Jev's probability (binary cross-entropy with a float label, as the Sentence-Transformers
cross-encoder trainer takes it). EPOCHS (default 1), BATCH (32), LR (2e-5) and MAX_LEN (256) are environment knobs; the
device is MPS when available. OUT.jsonl is a judge_ab.py reader over the gold candidates: the sigmoid of the tuned
logit. Needs torch and transformers.
"""
import json
import os
import random
import sys
import time

import torch
from transformers import AutoModelForSequenceClassification, AutoTokenizer

EPOCHS = int(os.environ.get("EPOCHS", "1"))
BATCH = int(os.environ.get("BATCH", "32"))
LR = float(os.environ.get("LR", "2e-5"))
MAX_LEN = int(os.environ.get("MAX_LEN", "256"))
SEED = 317

base, pool_cand, pool_jev, gold_cand, out_path = sys.argv[1:6]
device = "mps" if torch.backends.mps.is_available() else "cpu"
torch.manual_seed(SEED)
tok = AutoTokenizer.from_pretrained(base)
model = AutoModelForSequenceClassification.from_pretrained(base, num_labels=1).to(device)

jev = {}
for r in map(json.loads, open(pool_jev)):
    if "error" not in r:
        for fid, s in zip(r["facts"], r["scores"]):
            jev[(r["id"], fid)] = s
train = []
for r in map(json.loads, open(pool_cand)):
    for c in r["candidates"] or []:
        if (r["id"], c["id"]) in jev:
            train.append((r["text"], c["content"], jev[(r["id"], c["id"])]))
random.Random(SEED).shuffle(train)
print(f"training pairs {len(train)}, device {device}", flush=True)

opt = torch.optim.AdamW(model.parameters(), lr=LR)
steps = EPOCHS * ((len(train) + BATCH - 1) // BATCH)
sched = torch.optim.lr_scheduler.LambdaLR(opt, lambda s: min(1.0, (s + 1) / (0.1 * steps)) * max(0.0, 1 - s / steps))
loss_fn = torch.nn.BCEWithLogitsLoss()
model.train()
t0, step = time.time(), 0
for epoch in range(EPOCHS):
    for i in range(0, len(train), BATCH):
        batch = train[i:i + BATCH]
        enc = tok([b[0] for b in batch], [b[1] for b in batch], truncation=True, max_length=MAX_LEN, padding=True,
                  return_tensors="pt").to(device)
        target = torch.tensor([b[2] for b in batch], dtype=torch.float32, device=device)
        loss = loss_fn(model(**enc).logits.view(-1), target)
        loss.backward()
        opt.step()
        sched.step()
        opt.zero_grad()
        step += 1
        if step % 100 == 0:
            print(f"step {step}/{steps} loss {loss.item():.4f} {time.time() - t0:.0f} s", flush=True)
print(f"trained in {time.time() - t0:.0f} s", flush=True)

model.eval()
with open(out_path, "w") as w, torch.no_grad():
    for r in map(json.loads, open(gold_cand)):
        cands = r["candidates"] or []
        if len(cands) < 2:
            continue
        enc = tok([r["text"]] * len(cands), [c["content"] for c in cands], truncation=True, max_length=MAX_LEN,
                  padding=True, return_tensors="pt").to(device)
        scores = torch.sigmoid(model(**enc).logits.view(-1)).tolist()
        w.write(json.dumps({"id": r["id"], "facts": [c["id"] for c in cands],
                            "provisional": [c["provisional"] for c in cands],
                            "scores": [round(s, 4) for s in scores]}) + "\n")
