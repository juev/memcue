#!/bin/bash
# run.sh IN: one claude -p call, prompt from IN, JSON envelope to IN.json (no tools, no settings/hooks).
env -u MEMCUE_API_URL -u MEMCUE_API_KEY -u MEMCUE_MODEL claude -p --model opus --tools "" --output-format json --setting-sources "" < "$1" > "$1.json" 2> "$1.err"
echo "$1 exit=$?"
