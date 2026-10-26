#!/bin/sh
# memcue.sh runs the memcue binary for the Claude Code hooks: memcue.sh COMMAND [ARGS...].
#
# The binary is, in order: MEMCUE_BIN; `memcue` on PATH when it is this plugin's release or newer; the copy this
# script installed in ${XDG_DATA_HOME:-~/.local/share}/memcue/<version>/; an older `memcue` on PATH, so the hooks keep
# working. session-start installs the plugin's release when none of them is current (MEMCUE_AUTO_INSTALL=0 turns that
# off), exports its directory on the session's PATH for the skill, and says what is missing instead of failing.
# Every other command exits quietly without a binary.

set -u

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
version=$(sed -n 's/^[[:space:]]*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
	"$root/.claude-plugin/plugin.json" | head -n 1)
data=${XDG_DATA_HOME:-$HOME/.local/share}/memcue
dir=$data/$version
installed=$dir/memcue
release=${MEMCUE_RELEASE_URL:-https://github.com/juev/memcue/releases/download}
logdir=${XDG_STATE_HOME:-$HOME/.local/state}/memcue

current() { "$1" version -minimum "$version" >/dev/null 2>&1; }

# resolve prints the binary to run, or nothing. The version is checked only when the installed copy competes with one
# on PATH: a prompt should not pay for an extra process in the usual setup with a single binary.
resolve() {
	if [ -n "${MEMCUE_BIN:-}" ] && [ -x "$MEMCUE_BIN" ]; then
		echo "$MEMCUE_BIN"
		return
	fi
	onpath=$(command -v memcue 2>/dev/null || true)
	if [ ! -x "$installed" ]; then
		echo "$onpath"
	elif [ -n "$onpath" ] && [ "$onpath" != "$installed" ] && current "$onpath"; then
		echo "$onpath"
	else
		echo "$installed"
	fi
}

log() {
	mkdir -p "$logdir" 2>/dev/null && printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" >>"$logdir/install.log"
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	else
		shasum -a 256 "$1" | cut -d ' ' -f 1
	fi
}

# install downloads the release of this plugin, checks it against the release's checksums.txt and moves it into
# place. A lock directory keeps two sessions from downloading at once; one left by a crash expires after ten minutes.
# A failed install is not retried for an hour: SessionStart waits for it, and an unreachable GitHub would otherwise
# delay the first answer of every session.
install() {
	[ "${MEMCUE_AUTO_INSTALL:-}" = 0 ] && return 1
	[ -x "$installed" ] && return 0
	case $(uname -s) in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) log "install $version: unsupported system $(uname -s)"; return 1 ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) log "install $version: unsupported machine $(uname -m)"; return 1 ;;
	esac
	if ! command -v curl >/dev/null 2>&1; then
		log "install $version: curl is not on PATH"
		return 1
	fi
	mkdir -p "$dir" || return 1
	if [ -n "$(find "$dir/.failed" -maxdepth 0 -mmin -60 2>/dev/null)" ]; then
		return 1
	fi
	if [ -d "$dir/.lock" ] && [ -n "$(find "$dir/.lock" -maxdepth 0 -mmin +10 2>/dev/null)" ]; then
		rmdir "$dir/.lock" 2>/dev/null
	fi
	if ! mkdir "$dir/.lock" 2>/dev/null; then
		log "install $version: another session is installing"
		return 1
	fi
	tmp=$(mktemp -d "$dir/.tmp.XXXXXX") || { rmdir "$dir/.lock"; return 1; }
	archive=memcue_${version}_${os}_${arch}.tar.gz
	ok=1
	if ! curl -fsSL --max-time 45 -o "$tmp/$archive" "$release/v$version/$archive" ||
		! curl -fsSL --max-time 15 -o "$tmp/checksums.txt" "$release/v$version/checksums.txt"; then
		log "install $version: download from $release/v$version failed"
		ok=0
	fi
	if [ $ok = 1 ]; then
		want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
		got=$(sha256 "$tmp/$archive")
		if [ -z "$want" ] || [ "$want" != "$got" ]; then
			log "install $version: checksum of $archive does not match checksums.txt"
			ok=0
		fi
	fi
	if [ $ok = 1 ] && ! { tar -xzf "$tmp/$archive" -C "$tmp" memcue && chmod 755 "$tmp/memcue" &&
		current "$tmp/memcue" && mv "$tmp/memcue" "$installed"; }; then
		log "install $version: $archive did not give a working memcue $version"
		ok=0
	fi
	rm -rf "$tmp"
	if [ $ok = 1 ]; then
		rm -f "$dir/.failed"
		log "install $version: installed $installed"
	else
		touch "$dir/.failed"
	fi
	rmdir "$dir/.lock"
	[ $ok = 1 ]
}

note() {
	printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}\n' "$1"
}

cmd=${1:-}
bin=$(resolve)
# The card comes in parts, one hook each, and the hooks run at once (#480): part 1 installs and tells what is missing,
# the others print nothing unless a current binary is already there.
if [ "$cmd" = session-start ] && [ "${2:-}" = -part ] && [ "${3:-}" != 1 ]; then
	{ [ -n "$bin" ] && current "$bin"; } || exit 0
	exec "$bin" "$@"
fi
if [ "$cmd" = session-start ]; then
	if [ -z "$bin" ] || ! current "$bin"; then
		install && bin=$(resolve)
	fi
	if [ -n "$bin" ] && [ "$bin" = "$installed" ] && [ -n "${CLAUDE_ENV_FILE:-}" ]; then
		# $PATH expands when the session sources the file, not here.
		# shellcheck disable=SC2016
		printf 'export PATH="%s:$PATH"\n' "$dir" >>"$CLAUDE_ENV_FILE"
	fi
	if [ -z "$bin" ]; then
		why="see $logdir/install.log for why the plugin could not"
		[ "${MEMCUE_AUTO_INSTALL:-}" = 0 ] && why="MEMCUE_AUTO_INSTALL=0 keeps the plugin from installing it"
		note "memcue: the binary is not installed, so this session has no long-term memory. Install memcue $version ($why)."
		exit 0
	fi
	if ! current "$bin"; then
		note "memcue: the binary is older than $version; update memcue to get the session card and the facts again after a compaction."
		exit 0
	fi
fi
[ -n "$bin" ] || exit 0
exec "$bin" "$@"
