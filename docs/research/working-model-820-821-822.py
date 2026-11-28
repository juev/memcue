"""Bounded synthetic function-call pilot; run from a Hermes checkout/environment.

Requires the Hermes OpenAI Codex credential and its Python dependencies. The
only model inputs are the literal instructions, tool schemas and scenarios
below. No installed memory provider, native agent tools or live fact base is
opened. JSONL goes to stdout; do not record diagnostics from the provider.
"""
import concurrent.futures
import json
import sys
import time

from openai import OpenAI
from agent.codex_headers import codex_cloudflare_headers
from hermes_cli.runtime_provider import resolve_runtime_provider

MODEL = 'gpt-6.2-sol'
NATIVE_SCHEMA = '--native-schema' in sys.argv
BASE = ('Before storing, ask what a later session would do differently because it knows this fact. '
        'Keep task progress, completed outcomes, and paths or procedures already documented in the repository in the session or repository docs. '
        'Store a reusable pitfall only when its cause and fix or workaround are verified and a later fresh checkout would hit it again. '
        'Store a standing rule or decision as source user only when the user explicitly states it as a lasting preference or asks you to remember it. '
        'Search first, and correct a stored fact instead of adding a second copy. Correct or retire a stored fact only when the user says it is wrong or a command or a file of this session shows it: a newer fact that says otherwise is not that evidence on its own.')
CAPTURE = ('Before adding or correcting fact text, name a concrete future action that changes and check that the claim is verified, reusable in its scope, and not already stored. '
           'Skip hypotheses, volatile state that can be checked at its source, and one-off outcomes; do store explicit standing instructions, rare reproducible pitfalls, and qualified research that changes a future choice.')
REVIEW = ('For a fact that matters to this task and conflicts with current evidence, check the authoritative source for the same scope. '
          'Correct what changed if the replacement is verified and preserve useful conditions; retire only if the claim is demonstrably false or superseded with no still-useful conditions. '
          'Only the user can revise their standing instructions. If evidence is missing or conflicting, leave memory unchanged. Re-read and reassess after a version conflict; never automatically retry.')
FORMAT = ('You are performing the given work. The case provides all available evidence and memory results. '
          'Use the applicable memory write tools if warranted; do not invent evidence or use other tools. If no write is warranted, finish without a tool call. '
          'For a retirement give the evidence-based reason in the retirement call. Treat retired text as not current evidence.')

# The action oracle is fixed before requests. User/rule cases also require source=user.
CASES = [
    ('capture', 'one-off', 'none', 'A transient 502 in a one-time staging deploy cleared on rerun. The task is done; no reproducible cause was found.'),
    ('capture', 'rare-pitfall', 'add', 'In a fresh checkout of Acme, a verified optional generator order silently unsets its input on Windows only. Two fresh checkout runs reproduced it; running prepare before generate fixed it. This workflow is undocumented.'),
    ('capture', 'user-rule', 'add', 'The user says: Always preserve Acme generated files when formatting this repository. This is a standing rule. Search found no matching rule.'),
    ('capture', 'same-lesson', 'none', 'A clean build confirmed codegen requires prepare first on Windows. Existing verified fact in the same scope: On Windows run prepare before codegen in a fresh checkout.'),
    ('capture', 'different-scope', 'add', 'Existing fact for Acme: tool X needs UTF-8. In the separate repo Beta you verify the same assumption holds for another tool X binary on two fresh checkouts. Its source is external and this is not otherwise documented. The future action is to set this mode before importing assets in Beta.'),
    ('review', 'partial', 'correct', 'Fact Acme-v3: On Windows run prepare before generate; v2 also needed the shim. You are editing this same Windows workflow. The authoritative maintained v3 guide confirms prepare is still required and the shim is now forbidden on v3. Preserve the Windows condition and the distinction for v2.'),
    ('review', 'wrong-host', 'none', 'Fact A concerns staging host alpha. Current output shows Beta uses a different setting. You are working on Beta and have no evidence from alpha.'),
    ('review', 'superseded', 'retire', 'Fact A states the old Acme hook is required on all supported installations. The maintained compatibility policy and a current test show the hook was removed from every supported installation. A separately stored active fact already covers its replacement. You are using the current hook.'),
    ('review', 'user-reversal', 'retire', 'The user explicitly withdraws their standing Acme instruction to run the slow lint before every change, with no replacement. Stored fact A v3 is exactly that instruction; you are deciding whether to run lint.'),
    ('review', 'missing', 'none', 'Two conflicting facts name the active Acme provider; the newer one is B. No current config or user confirmation is available. You are deciding which provider to use.'),
    ('review', 'version-conflict', 'none', 'Your earlier retirement call for fact A v3 returned a version conflict. You do not have a fresh read; finish this step without inventing one.'),
    ('retired', 'repeat', 'none', 'The user asks if Acme used to require a renderer hook. An old comment says ENABLE_HOOK=true is required; you have no fresh verification of its current validity. The candidate claim is “Acme requires ENABLE_HOOK=true.”'),
    ('retired', 'recurrence', 'add', 'Acme v9 has reintroduced the hook; the authoritative current guide plus two fresh runs confirm ENABLE_HOOK=true is again required. No active fact has this verified finding. The candidate claim is “Acme requires ENABLE_HOOK=true.”'),
]
REASON = 'Previously retired because the v8 renderer removed the hook. This is not current evidence.'


def spec(name, fields):
    props = {k: {'type': 'string'} for k in fields}
    if NATIVE_SCHEMA:
        if 'source' in props:
            props['source']['enum'] = ['agent', 'tool', 'user', 'user_confirmed']
            props['source']['description'] = 'user for an explicit standing instruction, tool for verified tool output.'
            props['scope']['enum'] = ['repo:acme', 'repo:beta', 'global']
        if 'reason' in props:
            props['reason']['description'] = 'Your specific current evidence; no copied private text.'
        if name in ('correct', 'retire'):
            props['expected_version'] = {'type': 'integer', 'minimum': 1}
    return {'type': 'function', 'name': name, 'description': f'{name} one scoped fact', 'strict': True,
            'parameters': {'type': 'object', 'properties': props,
                           'required': list(props), 'additionalProperties': False}}


def run(client, case, arm):
    group, name, expected, context = case
    instructions = FORMAT + '\n' + BASE
    if NATIVE_SCHEMA:
        context += '\nFixture: any retrieved fact in this case has observed version 3.'
    if arm != 'baseline':
        instructions += '\n' + (CAPTURE if group == 'capture' else REVIEW)
    if group == 'retired' and arm != 'baseline':
        context += '\n' + ('Search returned one retired match: ' if arm == 'search' else 'Your pre-save check returned one retired match: ') + REASON
    tools = [spec('add', ['content', 'scope', 'source']), spec('correct', ['id', 'content']), spec('retire', ['id', 'reason'])]
    start = time.monotonic()
    try:
        with client.responses.stream(model=MODEL, instructions=instructions, input=[{'role': 'user', 'content': context}], tools=tools,
                                     parallel_tool_calls=False, store=False, reasoning={'effort': 'low'}) as stream:
            reply = stream.get_final_response()
        calls = [{'name': o.name, 'args': json.loads(o.arguments)} for o in reply.output if o.type == 'function_call']
        actions = [c['name'] for c in calls] or ['none']
        valid = actions == [expected] and (name != 'user-rule' or calls[0]['args']['source'] == 'user')
        if NATIVE_SCHEMA and valid and expected == 'add':
            target_scope = 'repo:beta' if name == 'different-scope' else 'repo:acme'
            valid = calls[0]['args']['scope'] == target_scope and calls[0]['args']['source'] == ('user' if name == 'user-rule' else 'tool')
        if NATIVE_SCHEMA and valid and expected in ('correct', 'retire'):
            valid = calls[0]['args']['expected_version'] == 3
        return {'group': group, 'case': name, 'arm': arm, 'expected': expected, 'calls': calls,
                'pass_action': valid, 'native_schema': NATIVE_SCHEMA, 'status': reply.status, 'returned_model': reply.model,
                'usage': reply.usage.model_dump() if reply.usage else None, 'ms': round(1000 * (time.monotonic() - start))}
    except Exception as exc:
        return {'group': group, 'case': name, 'arm': arm, 'status': 'error', 'error_type': type(exc).__name__,
                'http_status': getattr(exc, 'status_code', None), 'code': getattr(exc, 'code', None),
                'param': getattr(exc, 'param', None), 'ms': round(1000 * (time.monotonic() - start))}


if __name__ == '__main__':
    runtime = resolve_runtime_provider(requested='openai-codex', target_model=MODEL)
    assert runtime['provider'] == 'openai-codex' and runtime['api_mode'] == 'codex_responses'
    key, url = runtime['api_key'], runtime['base_url']
    client = OpenAI(api_key=key, base_url=url, default_headers=codex_cloudflare_headers(key, base_url=url),
                    timeout=45, max_retries=0)
    plan = [(case, arm) for case in CASES for arm in (('baseline', 'search', 'save') if case[0] == 'retired' else ('baseline', 'standing'))]
    with concurrent.futures.ThreadPoolExecutor(max_workers=3) as pool:
        for result in pool.map(lambda args: run(client, *args), plan):
            print(json.dumps(result), flush=True)
