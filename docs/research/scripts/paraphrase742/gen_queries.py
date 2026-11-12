"""memcue #742: the query writer. One model call per target sees that one fact only; one call per group writes the
negative and sees the group's three facts, so that it can ask what none of them answers.

The writer never sees a ranker, the other groups or the other targets. The stratum it aims at is only its intent:
measure_overlap puts every query in the stratum its measured overlap says.

Usage: gen_queries.py FIXTURE OUT.jsonl [MODEL]
"""
import json
import sys
from concurrent.futures import ThreadPoolExecutor

from llm import claude

fixture_path, out_path = sys.argv[1:3]
model = sys.argv[3] if len(sys.argv) > 3 else "claude-opus-5-5"
facts = json.load(open(fixture_path))["facts"]

LANG = {"en": "English", "ru": "Russian"}
CONTENT = ("A content word is any word except function words (articles, prepositions, conjunctions, pronouns, "
           "auxiliary verbs, question words). Different forms of one word are the same word: 'backup' and "
           "'backups', 'диск' and 'диска'. Names, paths, numbers and identifiers are content words too.")

TARGET_SYSTEM = f"""You write test queries for a search over an engineer's long-term memory: short notes a coding \
agent keeps about hosts, services and past problems. You get one note. Picture a person who needs exactly what \
this note says, before they have read it: they know their situation, not the note's wording. Write what that \
person would type into the memory search, as a question or a short request.

{CONTENT}

Write, all asking for the information this note gives:
- literal: 1 query that reuses the note's own key words, the way someone who half-remembers the note would ask.
- low: 2 queries in the note's language that share at most ONE content word with the note.
- zero: 2 queries in the note's language that share NO content word with the note: use synonyms, describe a \
thing instead of naming it (the role of a host or service, not its name), or describe the problem the person has \
instead of the fix the note holds.
- cross: 2 queries in the OTHER language ({LANG['en']} if the note is {LANG['ru']}, {LANG['ru']} if it is \
{LANG['en']}), written as a native speaker of that language would ask from the need, not as a translation of \
the note.

Each query must still point at this particular note: keep the detail that tells it apart from other notes on the \
same subject. Do not answer the question inside the query. Return JSON only."""

TARGET_SCHEMA = {
    "type": "object", "additionalProperties": False,
    "required": ["literal", "low", "zero", "cross"],
    "properties": {k: {"type": "array", "items": {"type": "string"}, "minItems": n, "maxItems": n}
                   for k, n in (("literal", 1), ("low", 2), ("zero", 2), ("cross", 2))},
}

NEGATIVE_SYSTEM = f"""You write test queries for a search over an engineer's long-term memory: short notes a coding \
agent keeps about hosts, services and past problems. You get the notes on one subject. Write 1 plausible query, in \
the notes' language, that someone working on this subject could type into the memory search, but that NONE of \
these notes answers: it asks for a detail on the same subject that the notes do not hold. Do not ask about \
something unrelated, and do not ask for something a note answers partly. Return JSON only."""

NEGATIVE_SCHEMA = {
    "type": "object", "additionalProperties": False, "required": ["negative"],
    "properties": {"negative": {"type": "string"}},
}


def target(f):
    out, meta = claude(model, TARGET_SYSTEM, TARGET_SCHEMA, f"Note ({LANG[f['lang']]}):\n{f['content']}\n")
    rows = []
    for intent in ("literal", "low", "zero", "cross"):
        for i, q in enumerate(out[intent]):
            lang = f["lang"] if intent != "cross" else ("ru" if f["lang"] == "en" else "en")
            rows.append({"cid": f"{f['id']}-{intent}-{i + 1}", "group": f["group"], "gold": [f["id"]],
                         "intent": intent, "lang": lang, "fact_lang": f["lang"], "text": q.strip(), "writer": meta})
    return rows


def negative(group):
    gf = [f for f in facts if f["group"] == group]
    notes = "\n".join(f"- {f['content']}" for f in gf)
    out, meta = claude(model, NEGATIVE_SYSTEM, NEGATIVE_SCHEMA, f"Notes ({LANG[gf[0]['lang']]}):\n{notes}\n")
    return [{"cid": f"{group}-negative-1", "group": group, "gold": [], "intent": "negative", "lang": gf[0]["lang"],
             "fact_lang": gf[0]["lang"], "text": out["negative"].strip(), "writer": meta}]


targets = [f for f in facts if f["role"] == "target"]
groups = sorted({f["group"] for f in facts})
with ThreadPoolExecutor(4) as ex:
    jobs = [ex.submit(target, f) for f in targets] + [ex.submit(negative, g) for g in groups]
    rows = [r for j in jobs for r in j.result()]
rows.sort(key=lambda r: r["cid"])
with open(out_path, "w") as w:
    for r in rows:
        w.write(json.dumps(r, ensure_ascii=False) + "\n")
calls = {(r["gold"][0] if r["gold"] else r["group"]): r["writer"]["cost_usd"] or 0 for r in rows}
print(len(rows), "candidates from", len(calls), "calls, cost", round(sum(calls.values()), 2), "USD")
