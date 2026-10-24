#!/bin/sh
# The size ladder of #325: every probe and memcue itself, built for linux/amd64 with the release flags (stripped) and
# without -s -w (for the symbol table), into OUT. Prints one line per binary: name, stripped bytes.
# usage: docs/research/scripts/binsize/ladder.sh OUT
out=${1:?usage: ladder.sh OUT}
mkdir -p "$out"
build() { # name package [extra build flags...]
	name=$1 pkg=$2
	shift 2
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags research -trimpath -ldflags "-s -w" "$@" -o "$out/$name" "$pkg" || exit 1
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags research -trimpath "$@" -o "$out/$name.sym" "$pkg" || exit 1
	printf '%-10s %10d\n' "$name" "$(wc -c < "$out/$name")"
}
d=./docs/research/scripts/binsize
build empty $d/empty
build sqlite $d/sqlite
build store $d/store
build hook $d/hook
build hookjev $d/hookjev
build memcue ./cmd/memcue
