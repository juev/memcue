"""Score the blind pairs with Jev the way TypeSafe documents duplicate checks: one fact in state, the other in
instructions, one condition per noul; no dates (which fact is older is code's job)."""
import json
import sys
import urllib.request
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

pairs_path, key_path, out_path, model = Path(sys.argv[1]), Path(sys.argv[2]), Path(sys.argv[3]), sys.argv[4]
key = key_path.read_text().strip()
pairs = json.loads(pairs_path.read_text())

QUESTIONS = {
    "same": ("potential_duplicate", "Does the potential duplicate state the same fact as the new record?",
             "Both state the same fact, possibly in other words, or one of them fully contains the other.",
             "They are merely on a similar topic or about the same system, but state a different fact or detail."),
    "contra": ("other_record", "Does the other record state a state of things that cannot be true together with the "
               "new record?",
               "Both cannot be true at once: the other record states a different, incompatible state of the same thing.",
               "Both can be true at once, or the other record is about something else."),
}


def ask(p):
    qs = {k: {"type": "noul", "instructions": {"question": q, field: p["b"]["text"]},
              "criteria": {"true": t, "false": f}} for k, (field, q, t, f) in QUESTIONS.items()}
    body = {"model": model, "state": {"new_record": p["a"]["text"]}, "questions": qs}
    req = urllib.request.Request("https://routerai.ru/api/v1/decisions", data=json.dumps(body).encode(),
                                 headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"})
    for _ in range(3):
        try:
            with urllib.request.urlopen(req, timeout=90) as r:
                a = json.load(r)
            return {"n": p["n"], "same": a["answers"]["same"]["noul"], "contra": a["answers"]["contra"]["noul"],
                    "tokens": a["usage"]["input_tokens"]}
        except Exception:  # retry transient errors
            pass
    return {"n": p["n"], "error": True}


with ThreadPoolExecutor(8) as ex:
    res = list(ex.map(ask, pairs))
out_path.write_text(json.dumps(res))
print("pairs", len(res), "errors", sum(1 for r in res if r.get("error")), "tokens", sum(r.get("tokens", 0) for r in res))
