"""memcue #742: the unique-answer check. A second model gets the whole corpus and one query and names every fact
that answers it. A positive query stays only when the answer is exactly its gold fact; a negative stays only when
no fact answers it. The checker sees fact IDs and texts, never the gold label, the group, the stratum or a ranker.

Usage: check_unique.py FIXTURE IN.jsonl OUT.jsonl [MODEL]
"""
import json
import sys
from concurrent.futures import ThreadPoolExecutor

from llm import claude

fixture_path, in_path, out_path = sys.argv[1:4]
model = sys.argv[4] if len(sys.argv) > 4 else "claude-sonnet-5-5"
facts = json.load(open(fixture_path))["facts"]
corpus = "\n".join(f"{f['id']}: {f['content']}" for f in facts)

SYSTEM = """You check test queries for a search over an engineer's long-term memory. You get every note in the \
memory, each with its ID, and one query someone typed. Decide which notes answer the query: a note answers it when \
it holds the information the query asks for, so that reading that note alone would give the person their answer. \
A note on the same subject that does not hold the asked detail does not answer. List the ID of every note that \
answers; give an empty list when none does. The query may be in another language than the notes. Return JSON only."""

SCHEMA = {
    "type": "object", "additionalProperties": False, "required": ["answers"],
    "properties": {"answers": {"type": "array", "items": {"type": "string"}}},
}


def check(row):
    out, meta = claude(model, SYSTEM, SCHEMA, f"Notes:\n{corpus}\n\nQuery:\n{row['text']}\n")
    answers = sorted(set(out["answers"]))
    row = dict(row, checker_answers=answers, checker=meta)
    row["unique_ok"] = answers == sorted(row["gold"])
    return row


rows = [json.loads(line) for line in open(in_path)]
with ThreadPoolExecutor(6) as ex:
    rows = list(ex.map(check, rows))
with open(out_path, "w") as w:
    for r in rows:
        w.write(json.dumps(r, ensure_ascii=False) + "\n")
print(len(rows), "checked,", sum(r["unique_ok"] for r in rows), "kept, cost",
      round(sum(r["checker"]["cost_usd"] or 0 for r in rows), 2), "USD")
