"""Normal-plugin request bridge. Only an initialized active provider owns a base."""
import json
import os
import hashlib
import weakref


# The card rides in the current turn's user message, the slot Hermes itself uses for ephemeral
# context (memory prefetch, `pre_llm_call` plugin context). Two reasons. Every transport accepts
# the user role, while a system/developer message the provider never expected costs the whole turn
# and silently moves it to a fallback provider (#730). And `system`/`instructions` precede the
# entire history, so a card that changes there misses the prompt cache for the whole conversation;
# the current turn's user message only moves the tail (#732). System, instructions, roles and tool
# output stay exactly as the payload sent them; a payload with no user text is left alone.
_FRAME_OPEN = '[memcue-context]'
_FRAME_CLOSE = '[/memcue-context]'
_SEPARATOR = '\n\n'
_TEXT_KEY = 'text'


class _TransientContext(dict):
    """Dict-only wire format; deepcopy preserves the weak, non-serialized owner."""
    def __init__(self, payload, owner):
        super().__init__(payload)
        self._owner = weakref.ref(owner)


def _framed(text):
    return f'{_FRAME_OPEN}\n{text}\n{_FRAME_CLOSE}'


def _without_trailing_frame(content):
    """Drop a frame this plugin appended to a string, matched on the exact form it writes."""
    if not content.endswith('\n' + _FRAME_CLOSE):
        return content
    start = content.rfind(_SEPARATOR + _FRAME_OPEN + '\n')
    if start >= 0:
        return content[:start]
    return '' if content.startswith(_FRAME_OPEN + '\n') else content


def _is_text_part(part):
    return isinstance(part, dict) and isinstance(part.get(_TEXT_KEY), str)


def _carries_user_text(message):
    """A user message with the user's own text, not a tool-result carrier (Anthropic, Bedrock)."""
    if not isinstance(message, dict) or message.get('role') != 'user':
        return False
    if message.get('type') not in (None, 'message'):
        return False
    content = message.get('content')
    return isinstance(content, str) or (isinstance(content, list) and any(map(_is_text_part, content)))


def _owned(part, owner):
    return isinstance(part, _TransientContext) and part._owner() is owner


def _with_card(content, text, owner):
    """The content with the card at its end, in the shape that content already has."""
    if isinstance(content, str):
        # A string carries no provenance, so only a frame at its very end is ours to replace.
        kept = _without_trailing_frame(content)
        return f'{kept}{_SEPARATOR}{_framed(text)}' if kept else _framed(text)
    # The shape comes from the whole list: an earlier card part may be the only text part it has.
    shape = next(part for part in reversed(content) if _is_text_part(part))
    kept = [part for part in content if not _owned(part, owner)]
    # ``cache_control`` marks a breakpoint, not a style: the fresh part never inherits the old one.
    fields = {key: value for key, value in shape.items() if key not in (_TEXT_KEY, 'cache_control')}
    return [*kept, _TransientContext({**fields, _TEXT_KEY: _framed(text)}, owner)]


def _target(messages):
    """Index of the current turn's user message: the last one carrying the user's own text."""
    return next((i for i in range(len(messages) - 1, -1, -1) if _carries_user_text(messages[i])), None)


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
    target = _target(request[key])
    if target is None:
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
    # The released dispatcher deep-copies requests. A weak owner attribute on an appended part survives
    # those copies, while dict/JSON serialization emits only provider-recognized keys.
    messages = list(request[key])
    message = messages[target]
    messages[target] = {**message, 'content': _with_card(message['content'], text, owner)}
    return {'request': {**request, key: messages}, 'source': 'memcue-context'}


def register(ctx):
    ctx.register_middleware('llm_request', request_context)
