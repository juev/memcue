"""Behavioral gates for the bounded native-only synthetic research controller."""
import copy
import tempfile
import unittest
from pathlib import Path

from native_preflight import (BASE, DEVELOPER, QUESTION, TOOL, SearchController, budget_status, inspect_request,
                              variant_catalog, create_output_directory, write_new_text, startup_check_argv)


def request():
    # Exact emitted namespace/function/schema; fixed synthetic messages only.
    return {'model': 'gpt-6.1-sol', 'reasoning': {'effort': 'high'},
            'input': [{'type': 'additional_tools', 'role': 'developer', 'tools': [
                {'type': 'namespace', 'name': 'functions', 'description': '', 'tools': [
                    {'type': 'function', 'name': TOOL['name'], 'description': TOOL['description'],
                     'strict': False, 'parameters': copy.deepcopy(TOOL['inputSchema'])}]}]},
                *[{'type': 'message', 'role': role, 'content': [
                    {'type': 'input_text', 'text': text}]} for role, text in [
                        ('developer', BASE), ('developer', DEVELOPER), ('user', QUESTION)]]]}


class NativePreflightTests(unittest.TestCase):
    def test_startup_config_has_no_conflicting_transport(self):
        argv = ['codex', 'app-server', '--strict-config', '--listen', 'stdio://', '-c', 'model="gpt-6.1-sol"']
        check = startup_check_argv(argv)
        self.assertNotIn('--listen', check)
        self.assertNotIn('stdio://', check)
        self.assertIn('--strict-config', check)
        self.assertIn('--print-startup-config', check)
        self.assertIn('model="gpt-6.1-sol"', check)
        self.assertIn('--listen', argv)

    def test_direct_only(self):
        self.assertTrue(inspect_request(request())['pass'])
        for tool in [{'type': 'function', 'name': 'exec'},
                     {'type': 'namespace', 'name': 'functions', 'tools': [
                         {'type': 'function', 'name': 'memory_search'}]},
                     {'type': 'web_search'}]:
            d = request(); d['input'][0]['tools'].append(tool)
            self.assertFalse(inspect_request(d)['pass'])

    def test_context_and_identity(self):
        for text in ['<environment_context>private</environment_context>',
                     '# AGENTS.md instructions', '## Skills', '.research-private/phase2-inputs']:
            d = request(); d['input'][1]['content'][0]['text'] = text
            self.assertFalse(inspect_request(d)['pass'])
        for key, value in [('model', 'other'), ('reasoning', {'effort': 'low'})]:
            d = request(); d[key] = value
            self.assertFalse(inspect_request(d)['pass'])

    def test_unknown_input_fails_closed(self):
        d = request(); d['input'].append({'type': 'unrecognized', 'text': 'opaque'})
        self.assertFalse(inspect_request(d)['pass'])

    def test_catalog_preserves_identity_and_instructions(self):
        m = {'slug': 'gpt-6.1-sol', 'model_messages': {'base': 'unchanged'},
             'tool_mode': 'code_mode_only', 'apply_patch_tool_type': 'freeform',
             'experimental_supported_tools': ['clock'], 'supports_search_tool': True}
        original = copy.deepcopy(m)
        variant, delta = variant_catalog(m)
        self.assertEqual(m, original)
        self.assertEqual(variant['model_messages'], m['model_messages'])
        self.assertEqual(variant['slug'], m['slug'])
        self.assertEqual(set(delta), {'tool_mode', 'apply_patch_tool_type',
                                     'experimental_supported_tools', 'supports_search_tool'})

    def test_dispatch_refuses_unexpected_invalid_and_fourth(self):
        calls = []; now = [0.0]
        c = SearchController(lambda q: calls.append(q) or ['actual result'], lambda: now[0])
        for tool, args in [('exec', {'query': 'x'}), ('memory_search', {'query': 'x', 'code': 'x'}),
                           ('memory_search', {'query': ''})]:
            with self.assertRaises(ValueError): c.dispatch(tool, args)
        self.assertEqual(calls, [])
        for _ in range(3): self.assertEqual(c.dispatch('memory_search', {'query': 'chosen'}), ['actual result'])
        with self.assertRaises(RuntimeError): c.dispatch('memory_search', {'query': 'fourth'})
        self.assertEqual(len(calls), 3)

    def test_deadline_before_and_after_search(self):
        now = [0.0]; calls = []
        def search(q):
            calls.append(q); now[0] = 61.; return []
        c = SearchController(search, lambda: now[0])
        with self.assertRaises(TimeoutError): c.dispatch('memory_search', {'query': 'x'})
        with self.assertRaises(TimeoutError): c.dispatch('memory_search', {'query': 'x'})
        self.assertEqual(len(calls), 1)
        self.assertEqual(c.executed, 1)

    def test_budget_unknown_overrun_and_units_override(self):
        self.assertEqual(budget_status([])['status'], 'UNKNOWN')
        self.assertEqual(budget_status([{}])['status'], 'UNKNOWN')
        self.assertEqual(budget_status([{'output_tokens': 4097}])['status'], 'OVERRUN')
        self.assertEqual(budget_status([{'output_tokens': 1, 'codex_rollout_budget_units': 5000}])['status'], 'OVERRUN')
        self.assertEqual(budget_status([{'output_tokens': 5000, 'codex_rollout_budget_units': 2}])['units'], 2)
        self.assertEqual(budget_status([{'output_tokens': 4096}])['status'], 'THRESHOLD_REACHED')
        self.assertEqual(budget_status([{'output_tokens': -1}])['status'], 'UNKNOWN')
        self.assertEqual(budget_status([{'codex_rollout_budget_units': float('nan')}])['status'], 'UNKNOWN')

    def test_output_collision_and_symlink(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td); out = create_output_directory(root/'owned')
            with self.assertRaises(FileExistsError): create_output_directory(out)
            write_new_text(out/'a', 'first')
            with self.assertRaises(FileExistsError): write_new_text(out/'a', 'second')
            (root/'link').symlink_to(out, target_is_directory=True)
            with self.assertRaises(FileExistsError): create_output_directory(root/'link'/'new')
            with self.assertRaises(FileExistsError): write_new_text(root/'link'/'new', 'x')
            self.assertEqual((out/'a').stat().st_mode & 0o777, 0o600)


if __name__ == '__main__': unittest.main()
