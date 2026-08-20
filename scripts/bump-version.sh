#!/usr/bin/env bash
# bump-version.sh — update the version in the Markdown sources for a new release.
#
# Usage: ./scripts/bump-version.sh <new-version>
# Example: ./scripts/bump-version.sh 2.1.0
#
# The script reads the current version from the VERSION file, replaces every
# occurrence of "v<old>" with "v<new>" in the listed files, then writes the
# new version back to VERSION.
#
# Files intentionally excluded:
#   CHANGELOG.md  — historical entries; updated manually

set -euo pipefail

if [ "${1:-}" = "" ]; then
  echo "Usage: $0 <new-version>"
  echo "Example: $0 2.1.0"
  exit 1
fi

NEW="$1"
OLD="$(cat VERSION | tr -d '[:space:]')"

if [ "$OLD" = "$NEW" ]; then
  echo "Already at version $NEW, nothing to do."
  exit 0
fi

echo "Bumping v$OLD → v$NEW"

# Only the hand-edited Markdown sources carry version strings now. The site's
# HTML and llms*.txt are rendered from these by scripts/build-docs.sh, which
# injects the version from the VERSION file at build time — so there is no
# committed HTML to update here.
FILES=(
  README.md
  LINESPEC.md
  PROVENANCE_RECORDS.md
)

for f in "${FILES[@]}"; do
  if [ -f "$f" ]; then
    # \Q...\E quotes the version string so dots aren't treated as regex wildcards.
    # Three patterns cover every place the version appears in the source docs:
    #   v<ver>          — prose, install commands, the "LineSpec v<ver>" title
    #   version-<ver>   — the shields.io badge (no leading "v")
    #   linespec_<ver>  — release artifact filenames (no leading "v")
    #   linespec:<ver>  — published proxy image tags (no leading "v"; release CI
    #                     strips it, so the docs must show the bare version)
    perl -pi -e "s/v\Q$OLD\E/v$NEW/g; s/version-\Q$OLD\E/version-$NEW/g; s/linespec_\Q$OLD\E/linespec_$NEW/g; s/linespec:\Q$OLD\E/linespec:$NEW/g" "$f"
    echo "  updated $f"
  fi
done

echo "$NEW" > VERSION
echo "Done. Don't forget to add a CHANGELOG.md entry for v$NEW."
