#!/usr/bin/env bash
# scripts/check-css-phantom-tokens.sh
#
# Catches "phantom design tokens": `var(--some-token, <literal>)` where
# `--some-token` is defined in NO theme file and NO stylesheet. When the
# custom property is undefined the literal fallback ALWAYS wins, so the
# declaration is a hardcoded value wearing a token costume — invisible in
# whichever theme the literal happens to suit, glaring in the other two.
#
# The bare form `var(--some-token)` with no fallback is worse: the
# declaration is invalid at computed-value time, so the property silently
# takes its inherited or initial value. `border: 1px solid var(--undefined)`
# renders NO border at all. Both forms are rejected here.
#
# A token counts as defined if any stylesheet declares it (`--x: value`,
# at any scope) OR any component sets it at runtime from JS via an inline
# style object or `setProperty`. The runtime case is detected, not
# allowlisted, so resize handles and animation-index properties keep
# working without ceremony.
#
# Fix by pointing at a token that exists in ALL FOUR themes (nex,
# nex-dark, noir-gold, nex-shell — see web/src/lib/themes.ts), not by
# defining a new token per call site. The shared vocabulary lives in
# web/src/styles/global.css `:root`, which every theme inherits; theme
# files in web/public/themes/*.css override only what differs.
#
# Escape hatch: scripts/css-phantom-token-allowlist.txt, which pins an
# exact occurrence count per file+token so a deferred site cannot quietly
# grow new siblings.
#
# Usage: bash scripts/check-css-phantom-tokens.sh
# Exit code: 0 clean, 1 violations found.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
web_root="$repo_root/web"
allowlist="$repo_root/scripts/css-phantom-token-allowlist.txt"

if [[ ! -d "$web_root" ]]; then
  echo "warn: no web/ directory; nothing to check"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

# Comments are stripped before every scan. A stylesheet header that
# *documents* this rule (and therefore spells out `var(--x, #hex)`) must
# not register as a usage. `//` is only stripped at the start of a line so
# that a URL inside a string survives.
strip_comments() { perl -0pe 's{/\*.*?\*/}{}gs; s{^[ \t]*//.*$}{}gm;' "$@"; }

# bash 3.2 (macOS) has no mapfile.
css_files=()
while IFS= read -r line; do css_files+=("$line"); done < <(
  find "$web_root/public/themes" "$web_root/src" -name '*.css' -type f 2>/dev/null | sort
)
ts_files=()
while IFS= read -r line; do ts_files+=("$line"); done < <(
  find "$web_root/src" \( -name '*.ts' -o -name '*.tsx' \) -type f 2>/dev/null | sort
)

if [[ ${#css_files[@]} -eq 0 ]]; then
  echo "warn: no stylesheets found under web/; nothing to check"
  exit 0
fi

# ── 1. Tokens declared by a stylesheet, at any scope ────────────────────
strip_comments "${css_files[@]}" \
  | grep -oE '(^|[;{[:space:]])--[A-Za-z0-9_-]+[[:space:]]*:' \
  | grep -oE -- '--[A-Za-z0-9_-]+' \
  | sort -u > "$tmp/defined"

# ── 2. Tokens set at runtime from JS ────────────────────────────────────
# Matches `style={{ "--x": v }}`, `["--x" as keyof CSSProperties]: v`, and
# `el.style.setProperty("--x", v)`.
if [[ ${#ts_files[@]} -gt 0 ]]; then
  { grep -hoE 'setProperty\([[:space:]]*["'"'"'`]--[A-Za-z0-9_-]+|\[?["'"'"'`]--[A-Za-z0-9_-]+["'"'"'`][^:]*:' "${ts_files[@]}" \
    || true; } \
    | { grep -oE -- '--[A-Za-z0-9_-]+' || true; } \
    | sort -u >> "$tmp/defined"
fi
sort -u "$tmp/defined" -o "$tmp/defined"

# ── 3. Every var(--x) usage, per file ───────────────────────────────────
# Components are scanned too, not just stylesheets: an inline
# `style={{ background: "var(--x, #0a0a0a)" }}` is the same bug in a place
# a CSS-only scan cannot see.
: > "$tmp/used"
for f in "${css_files[@]}" "${ts_files[@]}"; do
  rel="${f#"$repo_root"/}"
  # A file with no var() usage at all is fine, not a pipeline failure.
  strip_comments "$f" \
    | { grep -oE 'var\([[:space:]]*--[A-Za-z0-9_-]+' || true; } \
    | { grep -oE -- '--[A-Za-z0-9_-]+' || true; } \
    | sort | uniq -c \
    | while read -r count token; do
        printf '%s %s %s\n' "$rel" "$token" "$count"
      done >> "$tmp/used"
done

# ── 4. Undefined usages ─────────────────────────────────────────────────
: > "$tmp/violations"
while read -r rel token count; do
  grep -qxF -- "$token" "$tmp/defined" || printf '%s %s %s\n' "$rel" "$token" "$count" >> "$tmp/violations"
done < "$tmp/used"

# ── 5. Subtract the allowlist ───────────────────────────────────────────
: > "$tmp/allowed"
if [[ -f "$allowlist" ]]; then
  sed -e 's/#.*//' -e 's/[[:space:]]*$//' "$allowlist" | grep -vE '^$' > "$tmp/allowed" || true
fi

status=0
: > "$tmp/report"
while read -r rel token count; do
  [[ -z "$rel" ]] && continue
  allowed_count="$(awk -v r="$rel" -v t="$token" '$1 == r && $2 == t { print $3 }' "$tmp/allowed")"
  if [[ "$allowed_count" == "$count" ]]; then
    continue
  fi
  if [[ -n "$allowed_count" ]]; then
    printf '%s: %s used %sx, allowlist pins %sx\n' "$rel" "$token" "$count" "$allowed_count" >> "$tmp/report"
  else
    printf '%s: %s used %sx, defined nowhere\n' "$rel" "$token" "$count" >> "$tmp/report"
  fi
  status=1
done < "$tmp/violations"

# An allowlist entry whose site was fixed is stale — fail so the entry gets
# removed in the same PR that removed the debt.
while read -r rel token count; do
  [[ -z "$rel" ]] && continue
  if ! awk -v r="$rel" -v t="$token" '$1 == r && $2 == t { found = 1 } END { exit !found }' "$tmp/violations"; then
    printf '%s: %s is allowlisted (%s) but no longer used — drop the entry\n' "$rel" "$token" "$count" >> "$tmp/report"
    status=1
  fi
done < "$tmp/allowed"

if [[ "$status" -ne 0 ]]; then
  echo "::error::phantom CSS design tokens — var(--X) for an --X no stylesheet defines"
  echo
  sort "$tmp/report"
  echo
  echo "A var() whose custom property is undefined does not read a token:"
  echo "  var(--x, #fff)  ->  always #fff, in every theme"
  echo "  var(--x)        ->  declaration dropped (no border, inherited color)"
  echo
  echo "Point at a token that exists in all four themes instead. The shared"
  echo "vocabulary is web/src/styles/global.css ':root', which every theme"
  echo "inherits; web/public/themes/*.css override only what differs."
  echo
  echo "If the property is genuinely supplied at runtime, set it from the"
  echo "component (style={{ \"--x\": value }}) — that is detected automatically."
  echo "Only a site that needs a human design call belongs in"
  echo "scripts/css-phantom-token-allowlist.txt."
  exit 1
fi

echo "css-phantom-tokens check OK (${#css_files[@]} stylesheets, ${#ts_files[@]} components, $(wc -l < "$tmp/defined" | tr -d ' ') tokens defined)"
