"""Normal-plugin request bridge. Only an initialized active provider owns a base."""
import json
import os
import re
import hashlib
import weakref


# System-level text has one slot the transport accepts, and it is not always a message: the Codex
# Responses preflight admits no message item beyond user/assistant (a developer-role item raises
# before the call), Anthropic and Bedrock keep system text outside `messages`, and a
# chat-completions payload rejects a role its own provider never used (DeepSeek-compatible
# endpoints answer 422 for `developer`). A rejected role is invisible from here: the turn dies on
# the primary and silently continues on an unrelated fallback provider. So the card goes into the
# slot this very payload uses for its prompt, in the role that payload already carries, and a shape
# with no such slot is left alone instead of inventing one.
_FRAME_OPEN = '[memcue-context]'
_FRAME_CLOSE = '[/memcue-context]'
_FRAME = re.compile(re.escape(_FRAME_OPEN) + r'.*?' + re.escape(_FRAME_CLOSE) + r'\s*', re.S)
_PROMPT_ROLES = {'system', 'developer'}
_TEXT_KEY = 'text'


class _TransientContext(dict):
    """Dict-only wire format; deepcopy preserves the weak, non-serialized owner."""
    def __init__(self, payload, owner):
        super().__init__(payload)
        self._owner = weakref.ref(owner)


def _framed(text):
    return f'{_FRAME_OPEN}\n{text}\n{_FRAME_CLOSE}'


def _unframed(value):
    """Drop the block an earlier pass left in a plain string slot, with its blank separator."""
    return _FRAME.sub('', value).rstrip() if isinstance(value, str) else value


def _framed_after(value, text):
    kept = _unframed(value)
    return f'{kept}\n\n{_framed(text)}' if kept else _framed(text)


def _text_blocks(carrier, text, api_mode):
    """Append the card as one text block, mirroring the block shape this payload already carries."""
    kept = []
    for block in carrier:
        if isinstance(block, dict) and isinstance(block.get(_TEXT_KEY), str):
            stripped = _unframed(block[_TEXT_KEY])
            if not stripped:
                continue
            block = {**block, _TEXT_KEY: stripped}
        kept.append(block)
    shape = next(
        (block for block in kept if isinstance(block, dict) and isinstance(block.get(_TEXT_KEY), str)), None)
    if shape is None:
        # Anthropic needs a typed text block; Bedrock Converse takes a bare ``text``.
        shape = {'type': 'text'} if 'anthropic' in str(api_mode or '') else {}
    # ``cache_control`` marks a breakpoint, not a style: the fresh block never inherits the old one.
    kept.append({**{key: value for key, value in shape.items() if key != 'cache_control'}, _TEXT_KEY: _framed(text)})
    return kept


def _slot(request, key, api_mode):
    """``(kind, field)`` for the prompt slot this payload uses, or ``None`` when it has none."""
    if isinstance(request.get('instructions'), str):
        return 'text', 'instructions'
    system = request.get('system')
    if isinstance(system, str):
        return 'text', 'system'
    if isinstance(system, list):
        return 'blocks', 'system'
    # Chat completions take a trailing message. An Anthropic or Bedrock payload would reject a
    # `system` role among its already-converted `messages`, so those modes never reach this branch.
    if key == 'messages' and not any(mode in str(api_mode or '') for mode in ('anthropic', 'bedrock')):
        return 'message', None
    return None


def _prompt_role(messages):
    """The role this chat-completions payload accepts: its own leading prompt role, else `system`."""
    role = messages[0].get('role') if messages and isinstance(messages[0], dict) else None
    return role if role in _PROMPT_ROLES else 'system'


def _with_card(request, key, kind, field, text, owner, api_mode):
    if kind == 'message':
        item = _TransientContext({'role': _prompt_role(request[key]), 'content': text}, owner)
        messages = [message for message in request[key] if not (
            isinstance(message, _TransientContext) and message._owner() is owner)]
        return {**request, key: [*messages, item]}
    if kind == 'blocks':
        return {**request, field: _text_blocks(request[field], text, api_mode)}
    return {**request, field: _framed_after(request[field], text)}


def request_context(request, session_id='', **kwargs):
    if not session_id:
        return None
    from hermes_constants import get_hermes_home
    from hermes_cli.config import load_config
    from plugins.memory import load_memory_provider
    from agent.runtime_cwd import resolve_agent_cwd
    def enabled():
        config = load_config()
        plugins = config.get('plugins') or {}
        return ((config.get('memory') or {}).get('provider') == 'memcue' and
                'memcue-context' in (plugins.get('enabled') or []) and
                'memcue-context' not in (plugins.get('disabled') or []))
    if not enabled():
        return None
    home = os.path.realpath(str(get_hermes_home()))
    candidate = load_memory_provider('memcue')
    if candidate is None or not hasattr(type(candidate), 'request_owner'):
        return None
    owner = type(candidate).request_owner(home, session_id)
    if owner is None:
        return None
    key = 'input' if isinstance(request.get('input'), list) else 'messages'
    if not isinstance(request.get(key), list):
        return None
    api_mode = kwargs.get('api_mode')
    slot = _slot(request, key, api_mode)
    if slot is None:
        return None
    ok, raw = owner._run(['session-card', '-pitfalls-json'],
                         stdin=json.dumps({'cwd':str(resolve_agent_cwd())}), timeout=10,
                         state_dir=os.path.join(owner._state, 'request-context', hashlib.sha256(os.path.abspath(owner._facts).encode()).hexdigest()[:16]))
    # I/O can outlive a switch/shutdown or policy change. Revalidate the capability before emission.
    if (os.path.realpath(str(get_hermes_home())) != home or
            type(candidate).request_owner(home, session_id) is not owner or not enabled()):
        return None
    text = 'Repository pitfalls unavailable; earlier snapshots do not establish current applicability.'
    if ok:
        try:
            snapshot = json.loads(raw)
            facts = snapshot['facts']
            if isinstance(facts, list) and all(isinstance(f, str) for f in facts):
                text = (snapshot.get('context') or 'No current repository pitfalls; this supersedes earlier snapshots.') + '\n\n' + '\n\n'.join(facts)
        except (ValueError, KeyError, TypeError):
            pass
    # The released dispatcher deep-copies requests. A weak owner attribute survives those copies but
    # dict/JSON serialization emits only provider-recognized keys; a framed block in a string slot
    # carries no metadata at all and is replaced, never stacked, by the next pass.
    return {'request': _with_card(request, key, *slot, text, owner, api_mode), 'source': 'memcue-context'}


def register(ctx):
    ctx.register_middleware('llm_request', request_context)
