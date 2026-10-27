#!/usr/bin/env bash
# Reproduce the synthetic part of the Git history study of #530: the workload, its measurement and the checks.
#
#   docs/research/scripts/gitgrowth/run.sh SCRATCH RESULTS
#
# SCRATCH and RESULTS must be new paths (a symlink counts as existing) whose parents exist; neither may lie inside
# the other, inside this source tree or contain it. The checks run on the resolved paths before anything is created.
# Both are created 0700. Everything, temporary files included (TMPDIR), is written under SCRATCH, and the result
# JSON goes to RESULTS. Nothing is removed: SCRATCH holds only synthetic data and stays for inspection. A run killed
# with SIGKILL leaves whatever it had written, all of it inside SCRATCH.
#
# Every GIT_* variable of the caller is dropped and git reads neither the system nor the global config, so no
# command here can be pointed at another repository or object store from the environment.
#
# GITGROWTH_QUICK=1 shrinks the workload and the checks to a smoke run of a couple of minutes; its numbers are not
# the report's.
set -uo pipefail

for v in $(compgen -e); do
	case $v in GIT_*) unset "$v" ;; esac
done
export GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 LC_ALL=C

if [ $# -ne 2 ]; then
	echo "usage: $0 SCRATCH RESULTS" >&2
	exit 2
fi
here=$(dirname "$(readlink -f "$0")")
tree=$(git -C "$here" rev-parse --show-toplevel) || exit 1
tree=$(readlink -f "$tree")

for p in "$1" "$2"; do
	if [ -e "$p" ] || [ -L "$p" ]; then
		echo "$p exists; give a new path" >&2
		exit 2
	fi
done
# readlink -f resolves every component but the last, which must not exist yet; a missing parent fails here.
scratch=$(readlink -f "$1") && results=$(readlink -f "$2") || {
	echo "the parent of SCRATCH or RESULTS does not exist" >&2
	exit 2
}

# overlap A B: one of A and B is the other or lies under it.
overlap() { case "$1/" in "$2"/*) return 0 ;; esac; case "$2/" in "$1"/*) return 0 ;; esac; return 1; }
for p in "$scratch" "$results"; do
	if overlap "$p" "$tree"; then
		echo "$p overlaps the source tree $tree; SCRATCH and RESULTS must be outside it" >&2
		exit 2
	fi
done
if overlap "$scratch" "$results"; then
	echo "SCRATCH and RESULTS must not contain each other" >&2
	exit 2
fi

umask 077
mkdir -m 700 "$scratch" || exit 1
if ! mkdir -m 700 "$results"; then
	rmdir "$scratch"
	exit 1
fi

mkdir "$scratch/bin" "$scratch/tmp" || exit 1
export TMPDIR=$scratch/tmp

workload=()
interrupt=(-n 40)
race=(-for 30s)
if [ "${GITGROWTH_QUICK:-}" = 1 ]; then
	workload=(-initial 60 -commits 6 -writes 12)
	interrupt=(-n 3)
	race=(-for 2s)
fi

go build -C "$tree" -buildvcs=false -tags research -o "$scratch/bin/peakrss" ./docs/research/scripts/gitgrowth/peakrss || exit 1
go build -C "$tree" -buildvcs=false -tags research -o "$scratch/bin/synth" ./docs/research/scripts/gitgrowth/synth || exit 1
go build -C "$tree" -buildvcs=false -trimpath -o "$scratch/bin/memcue" ./cmd/memcue || exit 1
export GITGROWTH_PEAKRSS=$scratch/bin/peakrss

step() { echo "== $*" >&2; }

step "synthetic workload"
"$scratch/bin/synth" workload -out "$scratch/synth" -result "$scratch/workload.json" "${workload[@]}" || exit 1
python3 -I "$here/synthetic.py" --root "$scratch/synth" --workload "$scratch/workload.json" \
	--out "$results/synthetic.json" || exit 1

base=$scratch/synth/sqlite-every/memory/facts.db
step "round trip"
"$scratch/bin/synth" roundtrip -db "$base" -out "$scratch/roundtrip" -memcue "$scratch/bin/memcue" \
	-result "$results/roundtrip.json" || exit 1
step "interrupted import"
"$scratch/bin/synth" interrupt -db "$base" -out "$scratch/interrupt" -result "$results/interrupt.json" \
	"${interrupt[@]}" || exit 1
step "raw copy during writes"
"$scratch/bin/synth" race -db "$base" -out "$scratch/race" -result "$results/race.json" "${race[@]}" || exit 1
step "merge semantics"
"$scratch/bin/synth" semantics -out "$scratch/semantics" -memcue "$scratch/bin/memcue" \
	-result "$results/semantics.json" || exit 1
step "check"
python3 -I "$here/check.py" "$results" || exit 1
step "tables"
python3 -I "$here/report.py" "$results" || exit 1
echo "results in $results; synthetic scratch kept in $scratch" >&2
