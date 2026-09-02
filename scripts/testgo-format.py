#!/usr/bin/env python3
"""Format `go test -json` for one package: compact output plus a skip census.

Why this exists
===============
`go test` without -v prints "ok" for a package in which every test SKIPPED. Opt-in
suites — the live gbrain contract tests, the provider integration tests — then read
as passing while never executing. That is how an unverified change ships behind a
green run, and it is indistinguishable from real coverage in the summary.

Reading `-json` gives the skip census without the wall of text `-v` produces: this
prints the same one-line-per-package result a human wants, the full output of any
FAILED test, and appends every skip to a file the runner summarises at the end.

Usage:  go test -json <pkg> | testgo-format.py <pkg> <skip-log-path>
Exit:   0 when the package passed, 1 otherwise (mirrors `go test`).
"""

import json
import sys
from collections import defaultdict


def main() -> int:
    pkg = sys.argv[1] if len(sys.argv) > 1 else "?"
    skip_log = sys.argv[2] if len(sys.argv) > 2 else ""

    # Buffer per-test output so a failure can be printed in full while passing
    # tests stay quiet. Keyed by test name; package-level output has no name.
    output = defaultdict(list)
    failed: list[str] = []
    skipped: list[str] = []
    package_lines: list[str] = []
    failed_pkg = False

    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            ev = json.loads(line)
        except json.JSONDecodeError:
            # A build error or a panic trace is not JSON. It is exactly what a
            # human needs, so pass it through rather than swallowing it.
            print(line)
            failed_pkg = True
            continue

        action = ev.get("Action", "")
        test = ev.get("Test", "")
        text = ev.get("Output", "")

        if action == "output":
            (output[test] if test else package_lines).append(text)
        elif action == "fail" and test:
            failed.append(test)
        elif action == "skip" and test:
            skipped.append(test)
        elif action == "fail" and not test:
            failed_pkg = True

    for name in failed:
        print(f"--- FAIL: {name}")
        for chunk in output.get(name, []):
            sys.stdout.write(chunk)

    # Package-level lines carry the "ok  <pkg>  1.2s" / "FAIL" summary and any
    # build diagnostics; keep them so output matches what `go test` would show.
    for chunk in package_lines:
        sys.stdout.write(chunk)

    if skip_log and skipped:
        with open(skip_log, "a", encoding="utf-8") as fh:
            for name in skipped:
                fh.write(f"{pkg} :: {name}\n")

    return 1 if (failed or failed_pkg) else 0


if __name__ == "__main__":
    sys.exit(main())
