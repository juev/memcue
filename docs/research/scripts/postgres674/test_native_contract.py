"""R1/R2 regressions against independently reviewed, preserved native delivery."""
import copy
import json
import os
from pathlib import Path
import unittest
from native_preflight import inspect_request
from test_native_preflight import request


@unittest.skipUnless(os.environ.get('PG674_NATIVE_REQUEST_FIXTURE'), 'preserved native fixture required')
class NativeContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.recorded = json.loads(Path(os.environ['PG674_NATIVE_REQUEST_FIXTURE']).read_text())

    def check_rejected(self, body):
        self.assertFalse(inspect_request(body)['pass'])

    def test_positive_exact_recorded_and_fixed_inputs(self):
        self.assertTrue(inspect_request(self.recorded)['pass'])
        self.assertTrue(inspect_request(request())['pass'])

    def test_r1_unknown_top_level_instructions(self):
        for value in ['UNCLASSIFIED_REVIEW_CONTROL_674', {'text': 'unknown'}, ['unknown']]:
            with self.subTest(shape=type(value).__name__):
                body = copy.deepcopy(self.recorded); body['instructions'] = value
                if isinstance(value, str): self.check_rejected(body)
                else:
                    # Malformed context must fail closed, not raise or certify.
                    try: result = inspect_request(body)['pass']
                    except (TypeError, ValueError): result = True
                    self.assertFalse(result)

    def test_r1_native_wrappers_require_full_authenticated_text(self):
        for tag in ['permissions instructions', 'collaboration_mode', 'rollout_budget']:
            body = copy.deepcopy(self.recorded); changed = 0
            for item in body['input']:
                for part in item.get('content', []):
                    if part['text'].startswith('<'+tag+'>'):
                        part['text'] = '<'+tag+'>UNCLASSIFIED_REVIEW_CONTROL_674</'+tag+'>'; changed += 1
            self.assertEqual(changed, 1)
            with self.subTest(tag=tag): self.check_rejected(body)

    def test_r1_placement_duplicates_and_prior_conversation(self):
        mutations = []
        body = copy.deepcopy(self.recorded); body['input'][1]['role'] = 'user'; mutations.append(body)
        body = copy.deepcopy(self.recorded); body['input'].append(copy.deepcopy(body['input'][1])); mutations.append(body)
        body = copy.deepcopy(self.recorded); body['input'][1], body['input'][3] = body['input'][3], body['input'][1]; mutations.append(body)
        body = copy.deepcopy(self.recorded); del body['input'][1]; mutations.append(body)
        body = copy.deepcopy(self.recorded); body['input'].append({'type': 'message', 'role': 'assistant', 'content': [{'type': 'output_text', 'text': 'prior answer'}]}); mutations.append(body)
        for field in ['previous_response_id', 'conversation', 'additional_context', 'unrecognized_context']:
            body = copy.deepcopy(self.recorded); body[field] = 'UNCLASSIFIED_REVIEW_CONTROL_674'; mutations.append(body)
        for i, body in enumerate(mutations):
            with self.subTest(mutation=i): self.check_rejected(body)

    def test_r2_schema_must_equal_emitted_contract(self):
        changes = [lambda p: p['properties']['query'].update(type='integer'),
                   lambda p: p.clear(), lambda p: p.update(required=[]),
                   lambda p: p.update(additionalProperties=True),
                   lambda p: p.update(additionalProperties=0),
                   lambda p: p['properties'].update(hidden={'type': 'string'}),
                   lambda p: p['properties']['query'].update(description='hidden context'),
                   lambda p: p.update(description='hidden context')]
        for i, change in enumerate(changes):
            body = copy.deepcopy(self.recorded)
            change(body['input'][0]['tools'][0]['tools'][0]['parameters'])
            with self.subTest(mutation=i): self.check_rejected(body)

    def test_r1_tool_and_namespace_context_and_callable_identity(self):
        for location, key, value in [('namespace', 'description', 'hidden context'),
                                     ('namespace', 'name', 'other'),
                                     ('tool', 'description', 'hidden context'),
                                     ('tool', 'name', 'exec'),
                                     ('tool', 'extra_context', 'hidden context')]:
            body = copy.deepcopy(self.recorded); namespace = body['input'][0]['tools'][0]
            target = namespace if location == 'namespace' else namespace['tools'][0]
            target[key] = value
            with self.subTest(location=location, key=key): self.check_rejected(body)
        body = copy.deepcopy(self.recorded)
        body['input'][0]['tools'].append(copy.deepcopy(body['input'][0]['tools'][0])); self.check_rejected(body)
        body = copy.deepcopy(self.recorded); body['tools'] = body['input'][0]['tools']; self.check_rejected(body)
        body = copy.deepcopy(self.recorded); body['input'][1]['content'][0]['description'] = 'hidden context'; self.check_rejected(body)


if __name__ == '__main__': unittest.main()
