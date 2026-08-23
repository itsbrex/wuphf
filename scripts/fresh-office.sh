#!/usr/bin/env bash
# fresh-office.sh — wipe a WUPHF runtime home back to first-run state and
# relaunch, WITHOUT losing the operator's provider credentials.
#
# Why this exists: a runtime home mixes two very different things. Most of it
# is disposable office state (roster, channels, tasks, wiki, built apps — the
# apps directory alone runs to five figures of files). But .wuphf/config.json
# also holds the operator's API key and their company name, which a naive
# `rm -rf` destroys and which cannot be recovered from anywhere else.
#
# So: preserve config.json, drop everything that makes the office "already
# onboarded", and leave the provider credential in place so the fresh wizard
# still detects a signed-in runtime.
#
# Usage:
#   scripts/fresh-office.sh                      # dry run: show what would go
#   scripts/fresh-office.sh --yes                # actually wipe
#   scripts/fresh-office.sh --yes --home <path>  # target a specific home
#   scripts/fresh-office.sh --yes --relaunch     # wipe, then start the stack
set -euo pipefail

HOME_DIR="${WUPHF_RUNTIME_HOME:-$HOME/.wuphf-demo-home}"
BIN="${WUPHF_BIN:-$HOME/.wuphf-bin/wuphf-dev}"
BROKER_PORT="${WUPHF_BROKER_PORT:-7899}"
WEB_PORT="${WUPHF_WEB_PORT:-7900}"
CONFIRM=0
RELAUNCH=0

while [ $# -gt 0 ]; do
  case "$1" in
    --yes) CONFIRM=1 ;;
    --relaunch) RELAUNCH=1 ;;
    --home) shift; HOME_DIR="$1" ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
  shift
done

W="$HOME_DIR/.wuphf"
if [ ! -d "$W" ]; then
  echo "no runtime home at $W — nothing to do" >&2
  exit 1
fi

# Everything that makes an office "already onboarded and populated". config.json
# is deliberately NOT in this list.
WIPE=(office team tasks apps wiki wiki.bak agent-scratch logs onboarded.json)

echo "runtime home : $HOME_DIR"
echo "preserving   : .wuphf/config.json (API key, company name, provider choice)"
echo "removing     :"
for item in "${WIPE[@]}"; do
  target="$W/$item"
  [ -e "$target" ] || continue
  if [ -d "$target" ]; then
    n=$(find "$target" -type f 2>/dev/null | wc -l | tr -d ' ')
    printf '  %-16s %s files\n' "$item/" "$n"
  else
    printf '  %-16s (file)\n' "$item"
  fi
done

if [ "$CONFIRM" -ne 1 ]; then
  echo
  echo "DRY RUN — nothing deleted. Re-run with --yes to do it."
  exit 0
fi

# Stop anything holding the ports before removing state underneath it.
for P in "$BROKER_PORT" "$WEB_PORT"; do
  for PID in $(lsof -nP -iTCP:"$P" -t 2>/dev/null || true); do
    CWD=$(lsof -a -p "$PID" -d cwd -Fn 2>/dev/null | grep '^n' | sed 's/^n//' || true)
    case "$CWD" in
      */Google\ Chrome*|*/Chrome*) continue ;;  # a browser holding the socket, not a server
    esac
    kill "$PID" 2>/dev/null || true
  done
done
sleep 1

BACKUP="$W/config.json.bak-$(date +%s)"
cp "$W/config.json" "$BACKUP"
echo "config backed up to $BACKUP"

for item in "${WIPE[@]}"; do
  rm -rf "${W:?}/${item:?}"
done
echo "wiped."

if [ "$RELAUNCH" -eq 1 ]; then
  [ -x "$BIN" ] || { echo "no binary at $BIN — build first: go build -o $BIN ./cmd/wuphf" >&2; exit 1; }
  WUPHF_RUNTIME_HOME="$HOME_DIR" WUPHF_BROKER_PORT="$BROKER_PORT" \
    nohup "$BIN" --web-port "$WEB_PORT" --no-open \
    > "$HOME_DIR/dev-stack.log" 2>&1 &
  echo "relaunched on http://localhost:$WEB_PORT (log: $HOME_DIR/dev-stack.log)"
fi
