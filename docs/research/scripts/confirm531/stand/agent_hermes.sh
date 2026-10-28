#!/bin/sh
# One Hermes run of the #531 stand, inside its container: agent_hermes.sh ARM (base or cand).
# The Hermes home lives in the container's /tmp and is lost with it: the provider key comes from the environment and
# is never written under /stand, whose records are kept. Records go to /stand/rec.
set -eu
arm=$1
# The image sets HERMES_WRITE_SAFE_ROOT=/opt/data, which denies every file write in the workspace; the first grid ran
# with it, and the runs that tried to write were redone with the workspace as the safe root.
export HOME=/tmp/home HERMES_HOME=/tmp/hermes MEMCUE_STATE_DIR=/stand/state STAND_WEB_LOG=/stand/rec/web.log \
	HERMES_WRITE_SAFE_ROOT=/stand/ws
mkdir -p "$HOME" "$HERMES_HOME/plugins" /stand/rec /stand/base /stand/state
cp /opt/stand/hermes-config.yaml "$HERMES_HOME/config.yaml"
cp -R "/opt/hermes-plugin-$arm" "$HERMES_HOME/plugins/memcue"
seed=$(python3 -I -c 'import json; print(json.load(open("/stand/run.json"))["seed"] or "")')
if [ -n "$seed" ]; then
	MEMCUE_FACTS=/stand/base/facts.db python3 -I /opt/stand/seed.py "$seed" > /stand/rec/seed.log 2>&1
fi
MEMCUE_FACTS=/stand/base/facts.db memcue dump /stand/base/facts.db > /stand/rec/base_before.txt 2>/dev/null ||
	: > /stand/rec/base_before.txt
cd /stand/ws
status=0
hermes -z "$(cat /stand/prompt.txt)" --provider deepseek -m deepseek-flash --usage-file /stand/rec/usage.json \
	< /dev/null > /stand/rec/answer.txt 2> /stand/rec/stderr.log || status=$?
hermes sessions export --format jsonl /stand/rec/session.jsonl > /stand/rec/export.log 2>&1 || :
memcue dump /stand/base/facts.db > /stand/rec/base_after.txt 2>/dev/null || : > /stand/rec/base_after.txt
echo "$status" > /stand/rec/exit
