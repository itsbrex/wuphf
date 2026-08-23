#!/usr/bin/env bash
# test-web.sh - canonical local Web unit/component test runner.
#
# Why this script exists: `web/package.json` uses Vitest, but `bun test`
# invokes Bun's native test runner and does not behave the same way for this
# suite. This wrapper gives agents and humans one root-level command for both
# full and focused Web test runs.
#
# Usage:
#   bash scripts/test-web.sh
#   bash scripts/test-web.sh src/api/platform.test.ts
#   bash scripts/test-web.sh web/src/api/platform.test.ts
#
# Exit code: Vitest's exit code, or 124 if the wall-clock guard fired.
#
# WALL-CLOCK GUARD
# ----------------
# Vitest's own `testTimeout` cannot catch a SYNCHRONOUS infinite loop. A React
# render cycle that never settles spins the worker at 100% CPU and blocks its
# event loop, so the 10s timer is never allowed to run and the suite hangs
# forever instead of failing. That has now cost multiple people real time, and
# a suite that hangs is worse than one that fails: nobody can say whether it is
# green. This bounds the run from OUTSIDE the worker, which is the only place a
# spin can be interrupted from.
#
# Override with WEB_TEST_TIMEOUT (seconds); 0 disables the guard.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_root="$repo_root/web"

cd "$web_root" || exit 2

WEB_TEST_TIMEOUT="${WEB_TEST_TIMEOUT:-600}"

# `timeout` is GNU coreutils; macOS ships it only as `gtimeout` via homebrew.
timeout_bin=""
if [ "$WEB_TEST_TIMEOUT" != "0" ]; then
  if command -v timeout >/dev/null 2>&1; then
    timeout_bin="timeout"
  elif command -v gtimeout >/dev/null 2>&1; then
    timeout_bin="gtimeout"
  fi
fi

run_guarded() {
  if [ -z "$timeout_bin" ]; then
    "$@"
    return $?
  fi
  # SIGTERM first, then SIGKILL 15s later: a spinning worker ignores TERM.
  set +e
  "$timeout_bin" --kill-after=15s "${WEB_TEST_TIMEOUT}s" "$@"
  local rc=$?
  set -e
  if [ "$rc" -eq 124 ] || [ "$rc" -eq 137 ]; then
    echo
    echo "::error::web suite exceeded ${WEB_TEST_TIMEOUT}s and was killed"
    echo
    echo "This is a HANG, not a slow suite. The usual cause is a synchronous"
    echo "infinite loop in a component render, which pins a worker at ~100% CPU"
    echo "and blocks the event loop so vitest's own testTimeout can never fire."
    echo
    echo "To find it:"
    echo "  1. Bisect by directory: bunx vitest run src/components/<dir>"
    echo "  2. Then by file, then by describe/it block."
    echo "  3. Confirm the mechanism with 'ps -A -o %cpu,command | grep vitest'"
    echo "     while it hangs — ~100% CPU means a spin, near 0% means a leaked"
    echo "     handle (socket/timer) instead, which is a different fix."
    echo
    echo "Leaked-handle sources are already blocked centrally: fetch and"
    echo "EventSource in web/tests/setup.ts, and happy-dom subresource loading"
    echo "(iframe/script/stylesheet) in the vitest block of web/vite.config.ts."
    return 124
  fi
  return "$rc"
}

if [ "$#" -eq 0 ]; then
  echo "=== vitest web full suite (guard: ${WEB_TEST_TIMEOUT}s) ==="
  run_guarded bun run test
  exit $?
fi

args=()
for arg in "$@"; do
  case "$arg" in
    "$repo_root"/web/*)
      args+=("${arg#"$repo_root"/web/}")
      ;;
    ./web/*)
      args+=("${arg#./web/}")
      ;;
    web/*)
      args+=("${arg#web/}")
      ;;
    *)
      args+=("$arg")
      ;;
  esac
done

echo "=== vitest ${args[*]} (guard: ${WEB_TEST_TIMEOUT}s) ==="
run_guarded bunx vitest run "${args[@]}"
exit $?
