# Hermes: preserve literal memcue frames in string messages (#829)

## Hypothesis and baseline

A first-pass user message can end in text identical to a memcue frame. The middleware must preserve that text while replacing its own snapshot on in-process redispatch. On baseline `b7b2820`, the string branch guessed ownership from a trailing delimiter, while structured parts already used an out-of-band ownership marker.

## Experiment

Use the released Hermes plugin loader and `apply_llm_request_middleware` with synthetic repository snapshots only. The test quotes an exact previous frame at the end of an ordinary user string. Acceptance is exact recovery of the original outgoing user text after removing only the newly attached card, on both chat and Codex Responses paths. Check an unchanged redispatch, a changed cwd, a failed read, and JSON serialization. Existing suite controls cover structured parts, history/tool chains and requests without a user text slot. No inference or live store is needed.

```sh
# Run on the candidate with a real Hermes distribution and its Python.
make build
MEMCUE_BIN="$PWD/bin/memcue" MEMCUE_HERMES_DISTRIBUTION="$HERMES_SOURCE" \
  "$HERMES_PYTHON" plugins/hermes/test_request_context.py -v
```

| Attempt | Result |
| --- | --- |
| Regression only against baseline | Failed the `first dispatch removed user-authored text` assertion as expected. |
| Expanded/frozen regression against baseline | Failed at the same assertion; no production edit had been made. |
| Same frozen regression with ownership on the outgoing string message | Both request-context tests passed in the source and installed layouts, including the real chat/Responses dispatcher cases and transport controls. |
| Broader Hermes discovery | 20 tests discovered: 9 passed and 11 opt-in cases skipped. |

## Decision and limits

Use the existing weak-owner mechanism on the outgoing message dictionary for string content and retain the original text on that object, not in the serialized payload. This keeps string content a string and preserves literal user frames without a registry or text parser. Ownership survives the Hermes dispatcher's in-process deep copies, not serialization: a JSON-round-tripped request has no trusted ownership and is treated as ordinary input, not edited by guessing at delimiters. The last successful test also checks the serialized wire shape. This is a local transport result, not a deployment or a model behavior experiment.
