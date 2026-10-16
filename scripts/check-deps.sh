#!/usr/bin/env bash
# Holds the module to its dependency rule: every package builds from
# agentsession, openresponses, the standard library and this module
# alone, with one exception. A package named sqlite (opencode/sqlite)
# may import modernc.org/sqlite, a SQLite written in Go, and what that
# imports in turn; no other package may import either, and none but the
# command may depend on a sqlite package at any depth, so the format
# and convert packages build without a driver.
#
#   GO=go scripts/check-deps.sh
#
# Exits non-zero, naming each offence, or when go list fails.
set -euo pipefail
GO=${GO:-go}

module=$($GO list -m)
own="^($module|github.com/ChristopherDavenport/agentsession|github.com/ChristopherDavenport/openresponses)(/|\$)"
fail=0

# Direct imports: a package outside the standard library and the
# module's siblings is modernc.org/sqlite, imported by a sqlite package.
imports=$($GO list -f '{{.ImportPath}}{{range .Imports}} {{.}}{{end}}' ./...)
while read -r pkg rest; do
  for imp in $rest; do
    first=${imp%%/*}
    case $first in *.*) ;; *) continue ;; esac
    [[ $imp =~ $own ]] && continue
    if [[ $imp == modernc.org/sqlite && $pkg == */sqlite ]]; then
      continue
    fi
    echo "$pkg imports $imp"
    fail=1
  done
done <<<"$imports"

# At any depth: only the command and the sqlite packages depend on a
# sqlite package of this module or on modernc.org.
deps=$($GO list -f '{{.ImportPath}}{{range .Deps}} {{.}}{{end}}' ./...)
while read -r pkg rest; do
  case $pkg in "$module"/cmd/* | */sqlite) continue ;; esac
  for dep in $rest; do
    if [[ $dep == "$module"/*/sqlite || $dep == modernc.org/* ]]; then
      echo "$pkg depends on $dep"
      fail=1
    fi
  done
done <<<"$deps"

# Every dependency outside the standard library and the siblings is one
# a sqlite package brings in.
sqlite=$($GO list ./...)
sqlite=$(grep '/sqlite$' <<<"$sqlite" || true)
allowed=""
if [[ -n $sqlite ]]; then
  allowed=$($GO list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' $sqlite)
  allowed=$(grep -Ev "$own" <<<"$allowed" | sort -u || true)
fi
all=$($GO list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./...)
all=$(grep -Ev "$own" <<<"$all" | sort -u || true)
extra=$(comm -23 <(printf '%s\n' "$all" | sed '/^$/d') <(printf '%s\n' "$allowed" | sed '/^$/d'))
if [[ -n $extra ]]; then
  echo "module depends on: $extra"
  fail=1
fi
exit $fail
