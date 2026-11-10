"""Explicit connection selection must survive the subprocess boundary."""
import importlib.util
import os
from pathlib import Path
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parents[1]
spec = importlib.util.spec_from_file_location('remote_adapter', ROOT / 'scripts' / 'memcue.py')
adapter = importlib.util.module_from_spec(spec)
spec.loader.exec_module(adapter)


class RemoteSelection(unittest.TestCase):
    def test_explicit_remote_transport_and_invalid_selection_are_preserved(self):
        config = {'facts': '/synthetic/facts', 'state_dir': '/synthetic/state', 'host': 'synthetic'}
        settings = {'MEMCUE_POSTGRES_DSN': 'explicit-invalid-fixture',
                    'MEMCUE_SERVER': 'memcue-server',
                    'MEMCUE_TAILSCALE_MODE': 'embedded',
                    'MEMCUE_TAILSCALE_STATE_DIR': '/synthetic/identity',
                    'MEMCUE_TAILSCALE_HOSTNAME': 'synthetic-client',
                    'MEMCUE_TAILSCALE_CONTROL_URL': 'https://control.invalid',
                    'MEMCUE_TAILSCALE_ALLOW_LOOPBACK_CONTROL': '1',
                    'MEMCUE_TAILSCALE_CONNECT_TIMEOUT': '5s'}
        with patch.dict(os.environ, {**settings, 'MEMCUE_API_URL': 'must-not-inherit'}, clear=True):
            child = adapter.child_env(config)
        for key, value in settings.items():
            self.assertEqual(child.get(key), value, key)
        self.assertNotIn('MEMCUE_API_URL', child)
        self.assertEqual(child['MEMCUE_FACTS'], config['facts'])
        with patch.dict(os.environ, {}, clear=True):
            local = adapter.child_env(config)
        self.assertNotIn('MEMCUE_POSTGRES_DSN', local)
        self.assertNotIn('MEMCUE_SERVER', local)
        self.assertNotIn('MEMCUE_TAILSCALE_MODE', local)


if __name__ == '__main__':
    unittest.main()
