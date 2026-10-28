#!/bin/sh
# Build the #531 stand images on the docker host: build.sh REPO HOST.
# REPO is the memcue checkout; HOST an ssh host with docker. The build context is assembled in a new local directory
# and copied to HOST:stand531/ctx-<time>; the images are tagged stand531-cc and stand531-hermes there. Nothing is
# deleted on either side.
set -eu
repo=$1
host=$2
claude_code_version=2.1.312
hermes_image=nousresearch/hermes-agent@sha256:9c5561579f3ffdf333401797094ac3a743738d6c25d40f4331c7bf77b694238d
stand=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
stamp=$(date -u +%Y%m%dT%H%M%SZ)
ctx=$(mktemp -d "${TMPDIR:-/tmp}/stand531-ctx-XXXXXX")/ctx
python3 -I "$stand/make_variants.py" "$repo" "$ctx"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -C "$repo" -trimpath \
	-ldflags "-X main.version=stand531-$(git -C "$repo" rev-parse --short HEAD)" -o "$ctx/memcue" ./cmd/memcue
cp "$stand/web_mcp.py" "$stand/seed.py" "$stand/agent_cc.sh" "$stand/agent_hermes.sh" "$stand/mcp-cc.json" \
	"$stand/hermes-config.yaml" "$stand/Dockerfile.cc" "$stand/Dockerfile.hermes" "$ctx/"
ssh "$host" "mkdir -p stand531 && test ! -e stand531/ctx-$stamp"
scp -q -r "$ctx" "$host:stand531/ctx-$stamp"
ssh "$host" "set -e; cd stand531/ctx-$stamp
docker network inspect stand531 > /dev/null 2>&1 || docker network create stand531 > /dev/null
docker build -q -f Dockerfile.cc --build-arg CLAUDE_CODE_VERSION=$claude_code_version -t stand531-cc .
docker build -q -f Dockerfile.hermes --build-arg HERMES_IMAGE=$hermes_image -t stand531-hermes .
docker run --rm --entrypoint sh stand531-cc -c 'claude --version; memcue version'
docker run --rm --entrypoint sh stand531-hermes -c 'hermes --version 2>/dev/null | sed -n 1p; memcue version'"
echo "context stand531/ctx-$stamp"
