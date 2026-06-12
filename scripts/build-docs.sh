#!/usr/bin/env bash
# build-docs.sh — render the linespec.dev site from Markdown source.
#
# Markdown is the single source of truth. This script renders the content pages
# from LINESPEC.md / PROVENANCE_RECORDS.md with pandoc, materializes the chrome
# pages (index / docs) from committed templates with the version injected, and
# regenerates llms.txt / llms-full.txt. Every output below is a build artifact
# (gitignored); only the Markdown, templates, and assets are committed.
#
# Usage: ./scripts/build-docs.sh
# Requires: pandoc on PATH (brew install pandoc / apt-get install pandoc).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

if ! command -v pandoc >/dev/null 2>&1; then
  echo "error: pandoc is required but not found on PATH" >&2
  echo "       install it with 'brew install pandoc' or 'apt-get install -y pandoc'" >&2
  exit 1
fi

VERSION="$(tr -d '[:space:]' < VERSION)"
DOCS="docs"
TPL="$DOCS/templates"

echo "Building docs for v$VERSION"

# --- Content pages: Markdown -> HTML via the shared pandoc shell -------------
render_content() {
  local src="$1" out="$2" toc_level="$3" metatitle="$4" metadesc="$5" \
        navtitle="$6" gs_href="$7" gs_label="$8" next_href="$9" next_label="${10}"

  pandoc "$src" \
    --from gfm \
    --to html5 \
    --standalone \
    --template "$TPL/content.html" \
    --lua-filter "$TPL/site-filter.lua" \
    -M toc_level="$toc_level" \
    --metadata title="$metatitle" \
    -V metatitle="$metatitle" \
    -V metadesc="$metadesc" \
    -V navtitle="$navtitle" \
    -V version="$VERSION" \
    -V gs_other_href="$gs_href" \
    -V gs_other_label="$gs_label" \
    -V next_href="$next_href" \
    -V next_label="$next_label" \
    -o "$DOCS/$out"
  echo "  rendered $DOCS/$out  (from $src)"
}

render_content PROVENANCE_RECORDS.md provenance.html 2 \
  "Provenance Records - LineSpec Documentation" \
  "Complete documentation for LineSpec Provenance Records - structured YAML artifacts for architectural decisions." \
  "Provenance Records" \
  "linespec.html" "Testing Guide" \
  "linespec.html" "LineSpec Testing"

render_content LINESPEC.md linespec.html 1 \
  "LineSpec DSL Reference - LineSpec Documentation" \
  "Complete DSL reference for LineSpec Testing - protocol-level integration testing." \
  "LineSpec Testing" \
  "provenance.html" "Provenance Guide" \
  "provenance.html" "Provenance Records"

# --- Chrome pages: committed HTML templates with the version injected --------
render_chrome() {
  local name="$1"
  sed "s/__VERSION__/$VERSION/g" "$TPL/$name" > "$DOCS/$name"
  echo "  rendered $DOCS/$name  (from $TPL/$name)"
}

render_chrome index.html
render_chrome docs.html

# --- llms.txt: curated index template with the version injected -------------
sed "s/__VERSION__/$VERSION/g" "$TPL/llms.txt" > "$DOCS/llms.txt"
echo "  rendered $DOCS/llms.txt  (from $TPL/llms.txt)"

# --- llms-full.txt: header + full Markdown corpus concatenated --------------
{
  cat <<EOF
# LineSpec — Full Documentation

> This file concatenates the full LineSpec v$VERSION documentation — the project overview (README), the Provenance Records reference, and the LineSpec Testing DSL reference — as a single Markdown corpus for AI agents. The curated index is at https://linespec.dev/llms.txt.



---

# ===== Project Overview (README) =====

EOF
  cat README.md
  cat <<EOF

---

# ===== Provenance Records Reference =====

EOF
  cat PROVENANCE_RECORDS.md
  cat <<EOF

---

# ===== LineSpec Testing (DSL) Reference =====

EOF
  cat LINESPEC.md
} > "$DOCS/llms-full.txt"
echo "  rendered $DOCS/llms-full.txt  (from README.md + PROVENANCE_RECORDS.md + LINESPEC.md)"

echo "Done."
