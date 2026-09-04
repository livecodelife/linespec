#!/usr/bin/env bash
#
# Run the Go tests for the package containing a given file.
#
# A provenance associated_spec must point at a file that exists, and `go test`
# takes a package rather than a file - so pointing it straight at a _test.go
# file fails with "named files must be .go files". This bridges the two: give it
# any file in a package and it tests that package.
#
#   scripts/go-test-for.sh pkg/proxy/oracle/tns_test.go
#
set -euo pipefail

if [ $# -eq 0 ]; then
    echo "usage: $0 <file-in-package> [more files...]" >&2
    exit 2
fi

# Several spec entries sharing one run_command arrive as several paths; reduce
# them to the set of packages so a package is tested once however many of its
# files were named.
pkgs=$(for f in "$@"; do printf './%s\n' "$(dirname "$f")"; done | sort -u)

# shellcheck disable=SC2086
exec go test $pkgs
