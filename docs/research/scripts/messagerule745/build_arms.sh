#!/usr/bin/env bash
# Build the three memcue binaries of #745 from one commit:
#
#   build_arms.sh COMMIT OUT_DIR
#
# current  the commit as it is: a query of more than eight words is a message, a fact must hold two of its terms and
#          reach 0.45 of the best BM25.
# one-term messageTerms = 1, the 0.45 floor unchanged.
# search   messageWords raised past any query, so no query reads as a message: floor 0.6, no two-term rule.
#
# Each arm is a clean `git archive` of COMMIT with one constant changed; the script fails if the change did not land.
set -euo pipefail
commit=$1
out=$(mkdir -p "$2" && cd "$2" && pwd)
repo=$(git rev-parse --show-toplevel)
file=internal/recall/search.go

build() {
	local arm=$1 from=$2 to=$3 src="$out/src-$1"
	rm -rf "$src"
	mkdir -p "$src"
	git -C "$repo" archive "$commit" | tar -x -C "$src"
	if [ -n "$from" ]; then
		grep -qxF "$from" "$src/$file" || { echo "$arm: '$from' not in $file" >&2; exit 1; }
		FROM=$from TO=$to perl -0pi -e 's/^\Q$ENV{FROM}\E$/$ENV{TO}/m' "$src/$file"
		grep -qxF "$to" "$src/$file" || { echo "$arm: patch did not apply" >&2; exit 1; }
	fi
	(cd "$src" && go build -trimpath -o "$out/memcue-$arm" ./cmd/memcue)
	echo "$arm $(shasum -a 256 "$out/memcue-$arm" | cut -d' ' -f1)"
}

build current "" ""
build one-term "const messageTerms = 2" "const messageTerms = 1"
build search "const messageWords = 8" "const messageWords = 1 << 30"
