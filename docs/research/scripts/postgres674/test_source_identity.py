"""The harness may advance while evaluated production bytes stay pinned."""
from contextlib import chdir
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from source_identity import sha256, verify_source, verify_bridge

HERE = Path(__file__).resolve().parent


class SourceIdentity(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(dir=os.environ['TMPDIR'])
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        # All Git settings and ignore files belong to this isolated fixture.
        config = self.root / 'global-config'
        config.write_text('')
        env = patch.dict(os.environ, {'GIT_CONFIG_GLOBAL': str(config),
                                     'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_COUNT': '0'})
        env.start()
        self.addCleanup(env.stop)
        self.git('init', '-q')
        self.git('config', 'user.email', 'test@example.invalid')
        self.git('config', 'user.name', 'Fixture')
        (self.root / 'internal').mkdir()
        (self.root / 'internal/source.go').write_text('package fixture\n')
        (self.root / 'go.mod').write_text('module fixture\n')
        self.commit()
        self.baseline = self.git('rev-parse', 'HEAD').strip()

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], text=True,
                                       stderr=subprocess.DEVNULL)

    def commit(self):
        self.git('add', '.')
        self.git('commit', '-qm', 'fixture')

    def test_committed_docs_and_research_are_allowed(self):
        before = verify_source(self.root, self.baseline)
        (self.root / 'docs').mkdir()
        (self.root / 'docs/research.txt').write_text('a committed harness')
        self.commit()
        identity = verify_source(self.root, self.baseline)
        self.assertEqual(identity['evaluated_revision'], self.baseline)
        self.assertNotEqual(identity['harness_revision'], self.baseline)
        self.assertEqual(len(identity['production_files']), 2)
        self.assertEqual(identity['production_digest'], before['production_digest'])

    def ignore(self, kind, pattern):
        if kind == 'repository':
            (self.root / '.gitignore').write_text(pattern + '\n')
            self.commit()
        elif kind == 'info':
            (self.root / '.git/info/exclude').write_text(pattern + '\n')
        else:
            ignores = self.root / 'global-ignore'
            ignores.write_text(pattern + '\n')
            os.environ.update(GIT_CONFIG_COUNT='1', GIT_CONFIG_KEY_0='core.excludesFile',
                              GIT_CONFIG_VALUE_0=str(ignores))

    def test_ignored_production_is_rejected_independent_of_ignore_authority(self):
        for kind in ('info', 'repository', 'global'):
            for name in ('internal/extra.go', 'internal/assets/extra.txt',
                         'cmd/tool/extra.s', 'plugins/native/extra.c',
                         'internal/native/extra.h', 'internal/native/extra.syso'):
                with self.subTest(ignore=kind, path=name):
                    self.ignore(kind, name)
                    p = self.root / name
                    p.parent.mkdir(parents=True, exist_ok=True)
                    p.write_text('unevaluated compiler input\n')
                    self.assertEqual(self.git('check-ignore', '--', name).strip(), name)
                    try:
                        with self.assertRaisesRegex(ValueError, 'production'):
                            verify_source(self.root, self.baseline)
                    finally:
                        p.unlink()

    def test_ignored_directory_includes_nested_and_symlink_inputs(self):
        self.ignore('info', 'internal/hidden/')
        hidden = self.root / 'internal/hidden'
        hidden.mkdir()
        p = hidden / 'nested.go'
        p.write_text('package fixture\n')
        with self.assertRaisesRegex(ValueError, 'production'):
            verify_source(self.root, self.baseline)
        p.unlink()
        p.symlink_to('../../go.mod')
        with self.assertRaisesRegex(ValueError, 'production'):
            verify_source(self.root, self.baseline)

    def test_go_discovers_ignored_go_embed_cgo_and_assembly_inputs(self):
        # Real compiler discovery, including a wildcard embed with a new member.
        assets = self.root / 'internal/assets'
        assets.mkdir()
        (assets / 'base.txt').write_text('evaluated\n')
        (self.root / 'internal/source.go').write_text(
            'package fixture\nimport "embed"\n//go:embed assets/*\nvar Assets embed.FS\n')
        native = self.root / 'internal/native'
        native.mkdir()
        (native / 'native.go').write_text('package native\nimport "C"\n')
        self.commit()
        self.baseline = self.git('rev-parse', 'HEAD').strip()
        before = verify_source(self.root, self.baseline)
        additions = {'internal/extra.go': ('GoFiles', 'package fixture\n'),
                     'internal/extra.s': ('SFiles', '// extra assembly input\n'),
                     'internal/assets/extra.txt': ('EmbedFiles', 'unevaluated\n'),
                     'internal/native/extra.c': ('CFiles', 'int extra = 1;\n'),
                     'internal/native/extra.h': ('HFiles', '#define EXTRA 1\n')}
        self.ignore('info', '\n'.join(additions))
        for name, (_, data) in additions.items():
            (self.root / name).write_text(data)
        for package in ('internal', 'internal/native'):
            result = subprocess.check_output(['go', '-C', str(self.root), 'list', '-json',
                                              './' + package],
                                             env=dict(os.environ, CGO_ENABLED='1'), text=True)
            listed = json.loads(result)
            for name, (field, _) in additions.items():
                if str(Path(name).parent) == package or field == 'EmbedFiles' and package == 'internal':
                    self.assertIn(str(Path(name).relative_to(package)), listed[field])
        with self.assertRaisesRegex(ValueError, 'production'):
            verify_source(self.root, self.baseline)
        for name in additions:
            (self.root / name).unlink()
        self.assertEqual(verify_source(self.root, self.baseline)['production_digest'],
                         before['production_digest'])

    def caller_fixture(self):
        here = self.root / 'docs/research/scripts/postgres674'
        here.mkdir(parents=True)
        (here / 'fixture.json').write_text('{"facts":[],"cases":[]}')
        (here / 'declaration.json').write_text(json.dumps({'source': self.baseline}))
        (here / 'sqlite_bridge.go').write_text('''package main
import ("encoding/json"; "os"; _ "fixture/internal")
var productionIdentity, bridgeSourceSHA256 string
func main() {
    _ = json.NewEncoder(os.Stdout).Encode(map[string]string{
        "production_digest": productionIdentity, "bridge_source_sha256": bridgeSourceSHA256})
}
''')
        return here

    def test_build_caller_rejects_ignored_input_before_build_or_output(self):
        import build_bridge
        here = self.caller_fixture()
        self.ignore('info', 'internal/extra.go')
        (self.root / 'internal/extra.go').write_text('package fixture\n')
        out = self.root / 'new-build'
        sentinel = self.root / 'protected'
        sentinel.write_bytes(b'protected source-gate sentinel\n')
        sentinel.chmod(0o640)
        before = (sentinel.read_bytes(), sentinel.stat().st_mode, sentinel.stat().st_mtime_ns)
        with patch.object(build_bridge, 'HERE', here), \
             patch.object(build_bridge, 'verify_source', lambda root: verify_source(root, self.baseline)), \
             patch.object(sys, 'argv', ['build_bridge.py', '--out', str(out)]), chdir(self.root):
            with self.assertRaisesRegex(ValueError, 'production'):
                build_bridge.main()
        self.assertFalse(out.exists(), 'source gate must run before output creation and go build')
        self.assertEqual((sentinel.read_bytes(), sentinel.stat().st_mode, sentinel.stat().st_mtime_ns), before)

    def test_build_caller_accepts_docs_commit_and_identifies_real_binary(self):
        import build_bridge
        here = self.caller_fixture()
        self.commit()
        out = self.root / 'new-build'
        with patch.object(build_bridge, 'HERE', here), \
             patch.object(build_bridge, 'verify_source', lambda root: verify_source(root, self.baseline)), \
             patch.object(sys, 'argv', ['build_bridge.py', '--out', str(out)]), chdir(self.root):
            build_bridge.main()
        record = json.loads((out / 'identity.json').read_text())
        self.assertEqual(record['source_identity']['evaluated_revision'], self.baseline)
        self.assertNotEqual(record['source_identity']['harness_revision'], self.baseline)
        self.assertEqual(record['native_bridge']['production_digest'],
                         verify_source(self.root, self.baseline)['production_digest'])

    def test_verification_callers_reject_before_bridge_model_or_database(self):
        import experiments
        import private_replay
        here = self.caller_fixture()
        self.ignore('info', 'internal/extra.go')
        (self.root / 'internal/extra.go').write_text('package fixture\n')
        args = SimpleNamespace(bridge=str(self.root / 'absent-bridge'))
        out = self.root / 'absent-output'
        for module, invoke in ((experiments, lambda: experiments.run(args, out)),
                               (private_replay, lambda: private_replay.freeze(args, out, [], {}))):
            with self.subTest(caller=module.__name__), \
                 patch.object(module, 'HERE', here), \
                 patch.object(module, 'verify_source', lambda root, *rest: verify_source(root, self.baseline)), \
                 patch.object(module, 'verify_bridge', side_effect=AssertionError('bridge reached')), \
                 patch.object(module, 'Embedder', side_effect=AssertionError('model reached')):
                with self.assertRaisesRegex(ValueError, 'production'):
                    invoke()
        self.assertFalse(out.exists())

    def test_committed_dirty_staged_and_new_production_are_rejected(self):
        p = self.root / 'internal/source.go'
        for state in ('dirty', 'staged', 'committed'):
            with self.subTest(state=state):
                p.write_text('package changed\n')
                if state == 'staged': self.git('add', '.')
                if state == 'committed': self.commit()
                with self.assertRaisesRegex(ValueError, 'production'):
                    verify_source(self.root, self.baseline)
                self.git('reset', '--hard', self.baseline)
        (self.root / 'internal/new.go').write_text('package fixture\n')
        with self.assertRaisesRegex(ValueError, 'production'):
            verify_source(self.root, self.baseline)

    def test_symlink_production_rejected(self):
        p = self.root / 'internal/source.go'
        text = p.read_text(); p.unlink()
        (self.root / 'copy').write_text(text)
        p.symlink_to('../copy')
        with self.assertRaisesRegex(ValueError, 'production'):
            verify_source(self.root, self.baseline)

    def test_wrong_native_bridge_rejected(self):
        bridge = self.root / 'bridge'
        bridge.write_text('#!/bin/sh\nprintf \'%s\\n\' \'{"production_digest":"wrong","bridge_source_sha256":"wrong"}\'\n')
        bridge.chmod(0o700)
        identity = verify_source(self.root, self.baseline)
        with self.assertRaisesRegex(ValueError, 'bridge'):
            verify_bridge(bridge, identity, self.root / 'internal/source.go')

    def test_native_bridge_requires_both_source_and_production_identity(self):
        source = self.root / 'internal/source.go'
        identity = verify_source(self.root, self.baseline)
        valid = dict(production_digest=identity['production_digest'],
                     bridge_source_sha256=sha256(source.read_bytes()))
        bridge = self.root / 'bridge'
        for stale in (None, 'production_digest', 'bridge_source_sha256'):
            with self.subTest(stale=stale):
                claimed = dict(valid)
                if stale: claimed[stale] = 'stale'
                bridge.write_text("#!/bin/sh\nprintf '%s\\n' '" + json.dumps(claimed) + "'\n")
                bridge.chmod(0o700)
                if stale:
                    with self.assertRaisesRegex(ValueError, 'bridge'):
                        verify_bridge(bridge, identity, source)
                else:
                    self.assertEqual(verify_bridge(bridge, identity, source)['production_digest'],
                                     identity['production_digest'])
        bridge.write_text("#!/bin/sh\nprintf '%s\\n' '" + json.dumps(valid) + "'\n")
        source.write_text('package newer\n')
        with self.assertRaisesRegex(ValueError, 'bridge'):
            verify_bridge(bridge, identity, source)
