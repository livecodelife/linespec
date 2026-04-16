#!/usr/bin/env bash
# bump-version.sh — update all doc/site version references to a new release.
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

FILES=(
  docs/index.html
  docs/linespec.html
  docs/docs.html
  docs/provenance.html
  README.md
  LINESPEC.md
)

for f in "${FILES[@]}"; do
  if [ -f "$f" ]; then
    # \Q...\E quotes the version string so dots aren't treated as regex wildcards
    perl -pi -e "s/v\Q$OLD\E/v$NEW/g" "$f"
    echo "  updated $f"
  fi
done

echo "$NEW" > VERSION
echo "Done. Don't forget to add a CHANGELOG.md entry for v$NEW."
