#!/usr/bin/env python3
"""Snapshot of the fact base as of a moment, and the same facts laid out as the pre-memcue file memory.

    snap.py base  --src facts.db --at 2026-10-22T10:00:00Z --out snap/facts.db
    snap.py files --db snap/facts.db --archive ~/.claude/archive/memory-2026-10-19 --out snap/store

base: every version created after --at is dropped and the derived index is emptied, so memcue rebuilds it on open
(store.ensureIndex). files: a fact whose origin names its source note goes back into that note, with the note's
frontmatter and index line from the archive; a fact without one goes to <tier>/extracted_<kind>.md.
"""
import argparse, os, re, shutil, sqlite3
from collections import OrderedDict, defaultdict

LIVE = """SELECT id, content, kind, scope, origin, expires FROM facts f
 WHERE version = (SELECT max(version) FROM facts WHERE id = f.id) AND status = 'active'
   AND review IN ('', 'provisional') ORDER BY created, id"""
NOTE = re.compile(r"\(([^()/\s]+)/([^()/\s]+\.md)\)\s*$")
INDEX = re.compile(r"^- \[[^\]]*\]\(([^)]+\.md)\)")


def cmd_base(a):
    os.makedirs(os.path.dirname(os.path.abspath(a.out)), exist_ok=True)
    shutil.copyfile(a.src, a.out)
    db = sqlite3.connect(a.out)
    db.execute("DELETE FROM facts WHERE created > ?", (a.at,))
    db.execute("INSERT INTO fact_fts(fact_fts) VALUES ('delete-all')")
    db.execute("DELETE FROM fact_index")
    db.execute("DELETE FROM fact_vectors")
    db.commit()
    n = db.execute(f"SELECT count(*) FROM ({LIVE})").fetchone()[0]
    db.execute("VACUUM")
    print(f"snapshot at {a.at}: {n} active facts")


def tier_of(scope, origin):
    if scope == "global" or scope.startswith(("host:", "service:")):
        return "_global"
    if scope.startswith("repo:"):
        tier = scope[5:]
    else:
        tier = origin.split(" ", 1)[0] if origin else ""
    tier = tier.rstrip("/").split("/")[-1]  # repo:juev/memcue -> memcue, ~/.claude -> .claude
    # working inside ~/.claude was the _global tier of memory-sync.sh
    return "_global" if tier in ("", ".claude", "_global") else tier


def frontmatter(path):
    text = open(path, encoding="utf-8").read()
    m = re.match(r"^---\n(.*?)\n---\n", text, re.S)
    return m.group(0) if m else ""


def cmd_files(a):
    db = sqlite3.connect(f"file:{a.db}?mode=ro", uri=True)
    notes = defaultdict(list)  # (tier, file) -> facts
    for fid, content, kind, scope, origin, expires in db.execute(LIVE):
        m = NOTE.search(origin or "")
        if m and os.path.exists(os.path.join(a.archive, m.group(1), m.group(2))):
            notes[(m.group(1), m.group(2))].append(content)
        else:
            notes[(tier_of(scope or "", origin or ""), f"extracted_{kind or 'other'}.md")].append(content)
    if os.path.exists(a.out):
        shutil.rmtree(a.out)
    tiers = defaultdict(list)
    for (tier, name), facts in notes.items():
        tiers[tier].append(name)
        os.makedirs(os.path.join(a.out, tier), exist_ok=True)
        src = os.path.join(a.archive, tier, name)
        if os.path.exists(src) and not name.startswith("extracted_"):
            head = frontmatter(src)
        else:
            kind = name[len("extracted_"):-3]
            head = (f"---\nname: {tier}-extracted-{kind}\ndescription: факты вида {kind}, извлечённые из сессий\n"
                    f"metadata:\n  type: project\n---\n")
        with open(os.path.join(a.out, tier, name), "w", encoding="utf-8") as f:
            f.write(head + "\n" + "\n".join(f"- {c}" for c in facts) + "\n")
    lines_total = 0
    for tier, names in tiers.items():
        have = set(names)
        out = []
        idx = os.path.join(a.archive, tier, "MEMORY.md")
        if os.path.exists(idx):
            for line in open(idx, encoding="utf-8"):
                m = INDEX.match(line)
                if m and m.group(1) not in have:
                    continue  # the note lost every fact
                out.append(line.rstrip("\n"))
        else:
            out.append(f"# {tier} Memory")
        extracted = sorted(n for n in names if n.startswith("extracted_"))
        if extracted:
            out += ["", "## Извлечено из сессий", ""]
            for n in extracted:
                out.append(f"- [{n[:-3]}]({n}) — {len(notes[(tier, n)])} фактов вида {n[len('extracted_'):-3]}")
        with open(os.path.join(a.out, tier, "MEMORY.md"), "w", encoding="utf-8") as f:
            f.write("\n".join(out) + "\n")
        lines_total += len(out)
    print(f"tiers {len(tiers)}, notes {len(notes)}, facts {sum(len(v) for v in notes.values())},"
          f" index lines {lines_total}, _global index {os.path.getsize(os.path.join(a.out, '_global', 'MEMORY.md'))} B")


ap = argparse.ArgumentParser()
sub = ap.add_subparsers(dest="cmd", required=True)
b = sub.add_parser("base"); b.add_argument("--src", required=True); b.add_argument("--at", required=True)
b.add_argument("--out", required=True); b.set_defaults(func=cmd_base)
f = sub.add_parser("files"); f.add_argument("--db", required=True); f.add_argument("--archive", required=True)
f.add_argument("--out", required=True); f.set_defaults(func=cmd_files)
args = ap.parse_args()
args.func(args)
