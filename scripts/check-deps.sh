#!/usr/bin/env bash
# Holds the module to its dependency rule: every package builds from
# agentsession, openresponses, the standard library and this module
# alone, with one exception. A package named sqlite (opencode/sqlite)
# may import modernc.org/sqlite, a SQLite written in Go, and what that
# imports in turn; no other package may import either, directly.
#
#   GO=go scripts/check-deps.sh
#
# Exits non-zero, naming each offence.
set -euo pipefail
GO=${GO:-go}

module=$($GO list -m)
own="^($module|github.com/ChristopherDavenport/agentsession|github.com/ChristopherDavenport/openresponses)(/|\$)"
fail=0

# Direct imports: a package outside the standard library and the
# module's siblings is modernc.org/sqlite, imported by a sqlite package.
while read -r pkg imports; do
  for imp in $imports; do
    first=${imp%%/*}
    case $first in *.*) ;; *) continue ;; esac
    [[ $imp =~ $own ]] && continue
    if [[ $imp == modernc.org/sqlite && $pkg == */sqlite ]]; then
      continue
    fi
    echo "$pkg imports $imp"
    fail=1
  done
done < <($GO list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}' ./...)

# Every dependency outside the standard library and the siblings is one
# a sqlite package brings in.
sqlite=$($GO list ./... | grep '/sqlite$' || true)
allowed=""
if [[ -n $sqlite ]]; then
  allowed=$($GO list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' $sqlite | grep -Ev "$own" | sort -u || true)
fi
all=$($GO list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... | grep -Ev "$own" | sort -u || true)
extra=$(comm -23 <(printf '%s\n' "$all" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))
if [[ -n $extra ]]; then
  echo "module depends on: $extra"
  fail=1
fi
exit $fail
