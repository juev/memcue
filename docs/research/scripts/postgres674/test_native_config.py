"""Opt-in installed-CLI strict-config guard: never starts inference or reads auth."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

from native_preflight import clean_environment


@unittest.skipUnless(os.environ.get('PG674_NATIVE_BINARY'), 'set PG674_NATIVE_BINARY to installed 0.166.0 binary')
class InstalledConfigTests(unittest.TestCase):
    def test_unknown_config_and_override_are_rejected(self):
        binary = Path(os.environ['PG674_NATIVE_BINARY']).resolve()
        with tempfile.TemporaryDirectory() as td:
            home = Path(td); (home/'tmp').mkdir(mode=0o700)
            env = clean_environment(home)
            version = subprocess.run([str(binary), '--version'], env=env,
                                     capture_output=True, text=True, timeout=5)
            self.assertEqual(version.stdout.strip(), 'codex-cli 0.166.0')
            argv = [str(binary), 'app-server', '--strict-config', '--print-startup-config']
            baseline = subprocess.run(argv, env=env, cwd=home, capture_output=True, text=True, timeout=10)
            # This obsolete setting was previously ignored without strict-config.
            (home/'config.toml').write_text('include_apply_patch_tool = false\n')
            config = subprocess.run(argv, env=env, cwd=home, capture_output=True, text=True, timeout=10)
            (home/'config.toml').unlink()
            override = subprocess.run(argv+['-c', 'include_apply_patch_tool=false'], env=env,
                                      cwd=home, capture_output=True, text=True, timeout=10)
            evidence = os.environ.get('PG674_CONFIG_TEST_EVIDENCE')
            if evidence:
                from outputs import write_new_text
                write_new_text(evidence, json.dumps({'version': version.stdout.strip(),
                    'baseline': {'argv': argv, 'exit': baseline.returncode, 'stderr': baseline.stderr},
                    'config': {'argv': argv, 'exit': config.returncode, 'stderr': config.stderr},
                    'override': {'argv': argv+['-c', 'include_apply_patch_tool=false'],
                                 'exit': override.returncode, 'stderr': override.stderr}}, indent=2))

            self.assertEqual(baseline.returncode, 0, baseline.stderr)
            self.assertNotEqual(config.returncode, 0)
            self.assertNotEqual(override.returncode, 0)


if __name__ == '__main__': unittest.main()
