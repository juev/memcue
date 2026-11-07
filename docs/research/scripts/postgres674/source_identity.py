"""Pin evaluated production bytes independently of the research commit."""
import hashlib
import json
from pathlib import Path
import stat
import subprocess

PRODUCTION = ('internal', 'cmd', 'plugins', 'go.mod', 'go.sum')
BASELINE = 'a5de0b989ceb1046f4933b19cae7ef60bed0ede2'


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args])


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def verify_source(root, baseline=BASELINE):
    root = Path(root)
    expected = {}
    records = git(root, 'ls-tree', '-rz', baseline, '--', *PRODUCTION).split(b'\0')
    for record in records:
        if not record: continue
        meta, name = record.split(b'\t'); mode, kind, blob = meta.decode().split()
        if kind != 'blob' or mode not in ('100644', '100755'):
            raise ValueError('unsupported production entry')
        name = name.decode()
        expected[name] = dict(mode=mode, blob=blob, sha256=sha256(git(root, 'cat-file', 'blob', blob)))
    current = set(git(root, 'ls-files', '-z', '--', *PRODUCTION).decode().split('\0')) - {''}
    # Refuse every extra file in these boundaries, including ignored embed,
    # Cgo and assembly inputs. Git ignore rules do not constrain the compiler.
    extra = git(root, 'ls-files', '--others', '-z', '--', *PRODUCTION)
    dirty = subprocess.run(['git', '-C', str(root), 'diff', '--quiet', 'HEAD', '--', *PRODUCTION]).returncode
    if dirty or extra or current != set(expected):
        raise ValueError('changed, dirty or untracked production paths')
    for name, record in expected.items():
        p = root / name
        s = p.lstat()
        executable = bool(s.st_mode & 0o111)
        if not stat.S_ISREG(s.st_mode) or executable != (record['mode'] == '100755') or sha256(p.read_bytes()) != record['sha256']:
            raise ValueError('production bytes or mode differ from evaluated baseline')
    # Compare committed trees too: a staged restoration must not hide a changed HEAD.
    if git(root, 'diff', '--name-only', baseline, 'HEAD', '--', *PRODUCTION):
        raise ValueError('committed production differs from evaluated baseline')
    compact = {name: row['sha256'] for name, row in sorted(expected.items())}
    digest = sha256(json.dumps(compact, sort_keys=True, separators=(',', ':')).encode())
    return dict(evaluated_revision=baseline, harness_revision=git(root, 'rev-parse', 'HEAD').decode().strip(),
                production_digest=digest, production_files=expected)


def verify_bridge(bridge, identity, source):
    bridge = Path(bridge)
    if bridge.is_symlink() or not bridge.is_file():
        raise ValueError('native bridge must be a regular built binary: '+str(bridge))
    try:
        claimed = json.loads(subprocess.check_output([str(bridge.absolute()), 'identity'], timeout=5))
    except (ValueError, subprocess.SubprocessError) as error:
        raise ValueError('native bridge identity unavailable') from error
    if claimed.get('production_digest') != identity['production_digest'] or claimed.get('bridge_source_sha256') != sha256(Path(source).read_bytes()):
        raise ValueError('native bridge was built for different production or harness source')
    return dict(**claimed, binary_sha256=sha256(bridge.read_bytes()))
