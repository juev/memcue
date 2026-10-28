#!/bin/sh
# One Claude Code run of the #531 stand, inside its container: agent_cc.sh ARM (base or cand).
# /stand is the run directory the runner prepared: ws/ (the workspace), prompt.txt, run.json, base/ (a chained run's
# base, else empty). Records go to /stand/rec. Nothing outside /stand and the container's own files is reachable.
set -eu
arm=$1
export HOME=/tmp/home MEMCUE_FACTS=/stand/base/facts.db MEMCUE_STATE_DIR=/stand/state \
	MEMCUE_BIN=/usr/local/bin/memcue MEMCUE_AUTO_INSTALL=0 STAND_WEB_LOG=/stand/rec/web.log \
	DISABLE_TELEMETRY=1 DISABLE_ERROR_REPORTING=1 CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 \
	CLAUDE_CODE_DISABLE_AUTO_MEMORY=1
mkdir -p "$HOME" /stand/rec /stand/base /stand/state
seed=$(python3 -I -c 'import json; print(json.load(open("/stand/run.json"))["seed"] or "")')
if [ -n "$seed" ]; then
	python3 -I /opt/stand/seed.py "$seed" > /stand/rec/seed.log 2>&1
fi
memcue dump "$MEMCUE_FACTS" > /stand/rec/base_before.txt 2>/dev/null || : > /stand/rec/base_before.txt
cd /stand/ws
status=0
claude -p "$(cat /stand/prompt.txt)" --output-format stream-json --verbose --model claude-opus-5-5 \
	--permission-mode bypassPermissions --setting-sources "" --strict-mcp-config --mcp-config /opt/stand/mcp-cc.json \
	--plugin-dir "/opt/plugin-$arm" --no-session-persistence --disallowedTools WebFetch,WebSearch \
	< /dev/null > /stand/rec/stream.jsonl 2> /stand/rec/stderr.log || status=$?
memcue dump "$MEMCUE_FACTS" > /stand/rec/base_after.txt 2>/dev/null || : > /stand/rec/base_after.txt
echo "$status" > /stand/rec/exit
