"""Explicit frozen-copy input and deterministic, unlabeled replay contexts."""
import hashlib
import json
from pathlib import Path
import re
import stat
from source_identity import sha256

MANIFEST_SHA256 = 'af40dad282183aafc6305651638284874283a6186881254586442d9f9526b626'


def verify_inputs(root):
    root = Path(root)
    manifest = root / 'manifest.json'
    if manifest.is_symlink() or sha256(manifest.read_bytes()) != MANIFEST_SHA256:
        raise ValueError('unexpected frozen manifest')
    data = json.loads(manifest.read_text())
    actual = {str(p.relative_to(root)) for p in root.rglob('*') if not p.is_dir()} - {'manifest.json'}
    if actual != set(data['sha256']):
        raise ValueError('unexpected frozen input members')
    for name, expected in data['sha256'].items():
        p = root / name
        if Path(name).is_absolute() or '..' in Path(name).parts or not stat.S_ISREG(p.lstat().st_mode) or p.stat().st_nlink != 1 or sha256(p.read_bytes()) != expected:
            raise ValueError('frozen input hash or link mismatch')
    return data


def language(text):
    if re.search('[\u0400-\u04ff]', text): return 'cyrillic'
    if re.search('[A-Za-z]', text): return 'latin'
    return 'other'


def select_contexts(records, fallback_host, today, quotas=None):
    quotas = quotas or dict(latin=64, cyrillic=63, other=1)
    unique = {}
    for row in records:
        text = row.get('query', '').strip()
        if not text: continue
        project = row.get('tier', '')
        host = row.get('host') or fallback_host
        if not host: raise ValueError('explicit caller host required')
        # Use the logged tier as a caller project key; historical flags are unknown.
        project = [project] if project else []
        query = dict(Text=text, N=10, MinScore=0, Project=project, Host=host,
                     In=[], Kinds=[], Since='', Expired=False, AllWords=False, Today=today)
        canonical = json.dumps(query, ensure_ascii=False, sort_keys=True, separators=(',', ':'))
        key = hashlib.sha256(canonical.encode()).hexdigest()
        unique.setdefault(key, dict(id=key, query=query, language=language(text), host_source='log' if row.get('host') else 'manifest'))
    selected = []
    for category, count in quotas.items():
        candidates = sorted((v for v in unique.values() if v['language'] == category), key=lambda v: v['id'])
        if len(candidates) < count: raise ValueError('insufficient contexts in fixed stratum')
        selected.extend(candidates[:count])
    return sorted(selected, key=lambda v: v['id'])


def candidate_history(facts):
    excluded = {'keywords', 'queries'}
    return [{k: v for k, v in row.items() if k not in excluded} for row in facts]


def latest_facts(facts):
    latest = {}
    for row in facts:
        if row['version'] > latest.get(row['id'], {}).get('version', 0):
            latest[row['id']] = row
    return latest
