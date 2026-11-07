"""Bounded output-collision regressions; victims are isolated scratch sentinels."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from outputs import new_output_directory

HERE = Path(__file__).resolve().parent


def snapshot(path):
    # Reading a sentinel must not itself change its access timestamp.
    fd = os.open(path, os.O_RDONLY | os.O_NOATIME)
    with os.fdopen(fd, 'rb') as source:
        data = source.read()
    info = path.stat()
    return data, (info.st_dev, info.st_ino, info.st_mode, info.st_nlink,
                  info.st_size, info.st_atime_ns, info.st_mtime_ns, info.st_ctime_ns)


class OutputSafety(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='pg674-output-', dir=os.environ['TMPDIR'])
        self.addCleanup(self.temp.cleanup)
        if os.environ.get("PG674_FIXTURE_ARCHIVES"):
            from archive_fixtures import archive
            self.addCleanup(archive, self.temp.name, os.environ["PG674_FIXTURE_ARCHIVES"])
        self.root = Path(self.temp.name)
        discovery = subprocess.run(['git', '-C', str(self.root), 'rev-parse', '--show-toplevel'],
                                   capture_output=True)
        self.assertNotEqual(discovery.returncode, 0, 'scratch must be outside Git discovery')
        self.env = {k: v for k, v in os.environ.items() if not k.startswith('MEMCUE_')}

    def driver(self, output):
        return subprocess.run([sys.executable, '-B', str(HERE / 'experiments.py'),
                               '--pgroot', str(self.root / 'absent-pg'),
                               '--model-cache', str(self.root / 'absent-model'),
                               '--bridge', str(self.root / 'absent-bridge'), '--out', str(output)],
                              env=self.env, capture_output=True, text=True, timeout=15)

    def test_experiments_refuses_existing_output_without_changing_failure_record(self):
        for kind in ['file', 'symlink', 'hardlink']:
            with self.subTest(kind=kind):
                out = self.root / kind
                out.mkdir()
                victim = self.root / (kind + '-victim')
                victim.write_bytes(b'original failure evidence\n')
                victim.chmod(0o640)
                target = out / 'failure-traceback.txt'
                if kind == 'file':
                    target = victim.rename(target)
                    victim = target
                elif kind == 'symlink':
                    target.symlink_to(victim)
                else:
                    os.link(victim, target)
                before = snapshot(victim)
                target_before = target.lstat()
                directory_before = out.stat()
                result = self.driver(out)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('FileExistsError', result.stderr)
                self.assertEqual(snapshot(victim), before, result.stderr)
                self.assertEqual(target.lstat(), target_before)
                self.assertEqual(out.stat(), directory_before)


    def test_owned_failure_logging_is_exclusive(self):
        for kind in ['new', 'file', 'symlink', 'hardlink', 'dangling']:
            with self.subTest(kind=kind):
                victim = self.root / (kind + '-victim')
                victim.write_bytes(b'prior failure record')
                out = self.root / kind
                with self.assertRaisesRegex(RuntimeError, 'bounded failure'):
                    with new_output_directory(out):
                        target = out / 'failure-traceback.txt'
                        if kind == 'file':
                            target = victim.rename(target)
                            victim = target
                        elif kind == 'symlink':
                            target.symlink_to(victim)
                        elif kind == 'hardlink':
                            os.link(victim, target)
                        elif kind == 'dangling':
                            target.symlink_to(self.root / 'absent-target')
                        before = snapshot(victim)
                        raise RuntimeError('bounded failure')
                self.assertEqual(snapshot(victim), before)
                if kind == 'new':
                    self.assertIn('RuntimeError: bounded failure', target.read_text())
                    self.assertEqual(target.stat().st_mode & 0o777, 0o600)
                self.assertFalse((self.root / 'absent-target').exists())

    def test_experiments_new_failure_is_logged_without_starting_services(self):
        out = self.root / 'new-output'
        result = self.driver(out)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('absent-bridge', result.stderr)
        record = out / 'failure-traceback.txt'
        self.assertIn('absent-bridge', record.read_text())
        self.assertEqual(record.stat().st_mode & 0o777, 0o600)
        self.assertFalse((out / 'pgdata').exists())

    def audit(self, output):
        return subprocess.run([sys.executable, '-B', str(HERE / 'audit.py'),
                               '--pgroot', str(self.root / 'absent-pg'),
                               '--evidence', str(self.root / 'absent-evidence'),
                               '--bridge', str(self.root / 'absent-bridge'), '--out', str(output)],
                              env=self.env, capture_output=True, text=True, timeout=15)

    def test_directory_outputs_refuse_symlink_aliases(self):
        for driver in [self.driver, self.audit]:
            for kind in ['existing', 'dangling', 'parent']:
                with self.subTest(driver=driver.__name__, kind=kind):
                    root = self.root / (driver.__name__ + kind)
                    root.mkdir()
                    victim = root / 'victim'
                    output = root / 'alias'
                    if kind == 'existing':
                        victim.mkdir()
                        output.symlink_to(victim, target_is_directory=True)
                    elif kind == 'dangling':
                        output.symlink_to(victim, target_is_directory=True)
                    else:
                        victim.mkdir()
                        output.symlink_to(victim, target_is_directory=True)
                        output = output / 'new-child'
                    before = victim.stat() if victim.exists() else None
                    result = driver(output)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('FileExistsError', result.stderr)
                    self.assertEqual(victim.stat() if victim.exists() else None, before)
                    if victim.exists():
                        self.assertEqual(list(victim.iterdir()), [])

    def test_directory_outputs_refuse_existing_file_or_empty_directory(self):
        for driver in [self.driver, self.audit]:
            for kind in ['file', 'directory']:
                with self.subTest(driver=driver.__name__, kind=kind):
                    output = self.root / (driver.__name__ + kind)
                    if kind == 'file':
                        output.write_bytes(b'original output sentinel')
                    else:
                        output.mkdir()
                    before = snapshot(output) if kind == 'file' else output.stat()
                    result = driver(output)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertIn('FileExistsError', result.stderr)
                    self.assertEqual(snapshot(output) if kind == 'file' else output.stat(), before)
                    if kind == 'directory':
                        self.assertEqual(list(output.iterdir()), [])

    def minimal_saved_inputs(self):
        # Small invented saved ranks exercise output publication, without a DB/model.
        evidence = self.root / 'saved-inputs'
        evidence.mkdir()
        fixture = json.loads((HERE / 'fixture.json').read_text())
        def save(name, value):
            (evidence / name).write_text(json.dumps(value))
        save('freeze.json', {'inputs': {name: hashlib.sha256((HERE / name).read_bytes()).hexdigest()
                                      for name in ['fixture.json', 'declaration.json']}})
        (evidence / 'sqlite').mkdir()
        save('sqlite/normalized.json', {'cases': [{'id': c['id'], 'filter': {}} for c in fixture['cases']]})
        rows = [{'arm': arm, 'case': c['id'], 'rep': rep,
                 'hits': [{'id': 'aux-only', 'version': 1}] if arm == 'sqlite-full' and c['id'] in ['aux-en', 'query-en'] else []}
                for arm in ['sqlite-full', 'sqlite-content', 'pg-lexical', 'pg-vector-exact', 'pg-hybrid-rrf60']
                for c in fixture['cases'] for rep in [0, 1]]
        save('sqlite/sqlite-results.json', [r for r in rows if r['arm'].startswith('sqlite')])
        save('pg-results.json', [r for r in rows if r['arm'].startswith('pg-')])
        save('integrity.json', dict(sqlite_history_equal=True, pg_remaining_fields_equal_frozen_history=True,
                                    pg_history_unchanged_after_retrieval=True))
        columns = 'id version content origin created status author expires kind scope source evidence review prompt_version channel'.split()
        save('pg-history.json', sorted([{k: f[k] for k in columns} for f in fixture['facts']], key=lambda f: (f['id'], f['version'])))
        save('vectors.json', {'facts': {}})
        save('failure-results.json', [])
        save('transport-results.json', [dict(route=route, phase=phase, end_to_end_ms=1, sql_ms=1, embed_ms=0)
                                        for route in ['wire', 'http'] for phase, count in [('cold-cli', 10), ('warm-session', 30)]
                                        for _ in range(count)])
        save('embedding-times.json', [dict(kind=kind, ms=1) for kind in ['passage', 'query']])
        return evidence

    def test_recompute_refuses_existing_output_aliases(self):
        evidence = self.minimal_saved_inputs()
        for kind in ['file', 'symlink', 'hardlink', 'dangling']:
            with self.subTest(kind=kind):
                victim = self.root / (kind + '-victim')
                victim.write_bytes(b'original aggregate record')
                output = self.root / (kind + '-output')
                if kind == 'file':
                    output = victim
                elif kind == 'symlink':
                    output.symlink_to(victim)
                elif kind == 'hardlink':
                    os.link(victim, output)
                else:
                    output.symlink_to(self.root / 'absent-target')
                before = snapshot(victim)
                result = subprocess.run([sys.executable, '-B', str(HERE / 'recompute.py'), str(evidence),
                                         '--output', str(output)], env=self.env, capture_output=True, text=True, timeout=10)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn('FileExistsError', result.stderr)
                self.assertEqual(snapshot(victim), before)
                self.assertFalse((self.root / 'absent-target').exists())

    def test_recompute_new_output_and_stdout_agree(self):
        evidence = self.minimal_saved_inputs()
        argv = [sys.executable, '-B', str(HERE / 'recompute.py'), str(evidence)]
        stdout = subprocess.run(argv, env=self.env, capture_output=True, text=True, check=True, timeout=10)
        output = self.root / 'new-aggregate.json'
        subprocess.run(argv + ['--output', str(output)], env=self.env, capture_output=True, check=True, timeout=10)
        self.assertEqual(output.read_text(), stdout.stdout)


if __name__ == '__main__':
    unittest.main()
