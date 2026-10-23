#!/usr/bin/env bash
# SessionStart hook: two-tier, machine-portable agent memory.
#
# Problem it solves: native memory lives in ~/.claude/projects/<encoded-cwd>/memory,
# keyed by the on-disk path (username + checkout location). That path differs per
# machine and sits under the gitignored projects/, so memory never syncs.
#
# This hook keys memory by REPO NAME instead and stores it in the tracked
# ~/.claude/memory/ tree, which rides the existing git remotes. It:
#   1. resolves the current repo to a stable tier name (repo basename, or _global
#      when working inside ~/.claude itself),
#   2. (re)points projects/<encoded-cwd>/memory at memory/<tier> via symlink, so the
#      native memory feature reads/writes the tracked store transparently,
#   3. injects the _global tier into every session (cross-project user prefs,
#      feedback, tooling refs) on top of the repo-specific tier.
#
# stdout from a SessionStart hook is injected as context. Always exit 0 — a memory
# glitch must never block a session.
set -uo pipefail

CLAUDE="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
STORE="${MEMSYNC_STORE:-$CLAUDE/memory}"  # e2e: the rendered snapshot
PROJ="$CLAUDE/projects"
part="${1:-1}"

input="$(cat)"
cwd="$(printf '%s' "$input" | python3 -c 'import json,sys
try: print(json.load(sys.stdin).get("cwd",""))
except Exception: print("")' 2>/dev/null)"
[ -z "$cwd" ] && cwd="$PWD"

# --- resolve tier name (basename of the MAIN worktree dir) -------------------
# Key memory by the repo's main working-dir name, NOT the remote URL. The dir name
# matches what we sort tiers under, and a renamed/diverging remote can't silently
# split a tier into two (the bug: dir server-migration, remote loffe-configs).
# git-common-dir collapses every linked worktree back to the one main repo, so all
# worktrees of a repo share a single tier instead of one-tier-per-branch.
mainroot=""
if git -C "$cwd" rev-parse --git-dir >/dev/null 2>&1; then
  common="$(git -C "$cwd" rev-parse --path-format=absolute --git-common-dir 2>/dev/null || true)"
  [ -n "$common" ] && mainroot="$(dirname "$common")"
  [ -z "$mainroot" ] && mainroot="$(git -C "$cwd" rev-parse --show-toplevel 2>/dev/null || true)"
fi

tier=""
if [ -n "$mainroot" ]; then
  if [ "$mainroot" = "$CLAUDE" ]; then
    tier="_global"                       # editing your own config == the global context
  else
    base="$(basename "$mainroot")"
    case "$base" in
      .*) tier="$(basename "$(dirname "$mainroot")")-${base#.}" ;;  # …/forgekeep/.github -> forgekeep-github
      *)  tier="$base" ;;
    esac
  fi
fi

[ -n "${MEMSYNC_TIER:-}" ] && tier="$MEMSYNC_TIER"  # e2e: tier of the original repo, not of the clone

# --- repair the symlink bridge ----------------------------------------------
# Applies to every tier, incl. _global: working inside ~/.claude must write to
# the shared _global store on every machine, not the machine-local silo.
link_preexisting=0
if [ -n "$tier" ]; then
  # slug = the session's launch cwd, encoded the same way the harness names its
  # projects/ dir (s/[/.]/-/g). Keyed by cwd, not repo root, so a session started
  # in a subdir still gets its native memory dir bridged to the tier.
  slug="$(printf '%s' "$cwd" | sed 's/[/.]/-/g')"
  link="$PROJ/$slug/memory"
  dst="$STORE/$tier"
  # Was the bridge already in place when the session started? If yes, the harness
  # read the tier natively and injecting it again below would duplicate it. If we
  # are the ones creating/repairing the link, the harness may have missed it.
  [ -L "$link" ] && [ "$(readlink "$link")" = "$dst" ] && link_preexisting=1
  mkdir -p "$dst" "$PROJ/$slug"
  if [ -L "$link" ]; then
    [ "$(readlink "$link")" = "$dst" ] || { rm -f "$link"; ln -s "$dst" "$link"; }
  elif [ ! -e "$link" ]; then
    ln -s "$dst" "$link"
  elif [ -d "$link" ] && [ -z "$(ls -A "$link" 2>/dev/null)" ]; then
    rmdir "$link"; ln -s "$dst" "$link"
  fi
  # a non-empty real dir at $link is an un-migrated silo: leave it, warn below.
fi

# --- inject the global tier --------------------------------------------------
gmem="$STORE/_global/MEMORY.md"
[ -f "$gmem" ] || exit 0

# In a _global session the native memory dir IS memory/_global, so the harness
# already injected MEMORY.md. Printing it again costs ~10 KB of context for a
# byte-identical copy. Stay silent — but only when the bridge predates us; if we
# just created it, the harness read nothing and this hook is the only source.
if [ "$tier" = "_global" ] && [ "$link_preexisting" = 1 ]; then
  exit 0
fi

# Hook stdout is capped at 10,000 characters; past that the harness swaps it for
# a 2,000-character preview and the rest of the index silently never arrives.
# The cap is per hook, so the index is cut into chunks on line boundaries and
# each registration of this script (argument = chunk number) prints one.
# config-audit.sh checks that the registrations cover the whole index.
chunk="$(python3 -c 'import sys
budget = 8800
chunks = [""]
for line in open(sys.argv[1], encoding="utf-8"):
    if chunks[-1] and len(chunks[-1]) + len(line) > budget:
        chunks.append("")
    chunks[-1] += line
n = int(sys.argv[2])
if 1 <= n <= len(chunks):
    sys.stdout.buffer.write(("%d\n%s" % (len(chunks), chunks[n - 1])).encode("utf-8"))' "$gmem" "$part" 2>/dev/null)"
[ -n "$chunk" ] || exit 0
total="${chunk%%$'\n'*}"
chunk="${chunk#*$'\n'}"

echo "<global-memory tier=\"${tier:-none}\" part=\"${part}/${total}\">"
if [ "$part" != 1 ]; then
  printf '%s\n' "$chunk"
  echo "</global-memory>"
  exit 0
fi
echo "Cross-project memory (applies everywhere), synced via the ~/.claude repo."
if [ -n "$tier" ] && [ "$tier" != "_global" ]; then
  echo "Repo-specific memory for this session lives in memory/${tier}/ (injected"
  echo "natively). Write routing: a fact specific to THIS repo -> the native memory"
  echo "dir as usual; a GLOBAL fact (user pref, how-to-work feedback, tooling ref)"
  echo "-> ${STORE}/_global/ and update ${STORE}/_global/MEMORY.md."
  if [ -L "$link" ] || [ ! -e "$link" ]; then :; else
    echo "⚠ ${link} is a non-empty real dir, not a symlink — un-migrated silo."
    echo "  Move its files into ${dst}/ and replace it with a symlink to sync."
  fi
fi
echo
printf '%s\n' "$chunk"
echo "</global-memory>"
exit 0
