GO ?= go
STATICCHECK ?= $(GO) run honnef.co/go/tools/cmd/staticcheck@latest
GOVULNCHECK ?= $(GO) run golang.org/x/vuln/cmd/govulncheck@latest

.PHONY: build deps test vet fmt tidy tidy-check lint vuln check corpus \
	release-guard release clean

build:
	$(GO) build ./...

# The module must build from agentsession, openresponses and the
# standard library alone; the format packages from the standard library
# alone, which TestImportBoundary checks.
deps:
	@deps=$$($(GO) list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' ./... \
	  | grep -v '^github.com/ChristopherDavenport/agentimport' \
	  | grep -v '^github.com/ChristopherDavenport/agentsession' \
	  | grep -v '^github.com/ChristopherDavenport/openresponses' || true); \
	  test -z "$$deps" || { echo "module depends on: $$deps"; exit 1; }

test:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

# Fails when go mod tidy would change go.mod or go.sum, without writing,
# so a stray dependency shows up in make check and not only in CI's diff.
tidy-check:
	$(GO) mod tidy -diff

fmt:
	gofmt -l . && test -z "$$(gofmt -l .)"

lint:
	$(STATICCHECK) ./...

vuln:
	$(GOVULNCHECK) ./...

# Everything CI runs.
check: fmt tidy-check vet deps lint vuln test

# Converts every session in a local corpus and checks each verifies and
# accounts for every line. Real sessions are private, so this reads them
# where they are and is not part of check:
#   make corpus CLAUDECODE=~/.claude/projects PI=~/.pi/agent/sessions CODEX=~/.codex/sessions ATIF=~/harbor/jobs \
#     CLINE=~/.config/Code/User/globalStorage/saoudrizwan.claude-dev
corpus:
	AGENTIMPORT_CORPUS_CLAUDECODE=$(CLAUDECODE) AGENTIMPORT_CORPUS_PI=$(PI) \
	  AGENTIMPORT_CORPUS_CODEX=$(CODEX) AGENTIMPORT_CORPUS_ATIF=$(ATIF) \
	  AGENTIMPORT_CORPUS_CLINE=$(CLINE) \
	  $(GO) test -count=1 -run TestCorpus ./...

MODULE := $(shell $(GO) list -m)

# Checks one tag is safe to push, before it is pushed. A pushed tag is
# permanent — the proxy and the checksum database keep the version
# forever — so this is the last point at which a mistake is free:
#   make release-guard TAG=v0.1.0
release-guard:
	@test -n "$(TAG)" || { echo "usage: make release-guard TAG=<tag>"; exit 1; }
	@scripts/release-guard.sh "$(TAG)"

# Cut a release: the changelog's Unreleased section is dated, everything
# is checked, one commit is made, the tag is guarded and then written
# with the changelog section as its message, and the branch and tag are
# pushed. TRAILER, when set, is appended to the commit message.
#
# The guard runs after the commit and before the tag, which is the last
# moment everything is still local: if it refuses, undo with git reset
# --hard HEAD~1. Nothing is public until the push.
#
# --atomic lands the branch and the tag in one transaction, so no window
# exists in which the tag is visible without the commit it names.
#
# The changelog is dated through a temp file rather than sed -i, which is
# a GNU-ism: BSD sed reads the argument after -i as a backup suffix, so
# the GNU spelling fails outright on macOS, where these releases are cut.
# The temp file is removed if sed dies, so a failed run leaves nothing
# untracked behind for the clean-tree gate to trip over next time.
release:
	@test -n "$(VERSION)" || { echo "usage: make release VERSION=vX.Y.Z"; exit 1; }
	@grep -q '^## Unreleased$$' CHANGELOG.md || { echo "CHANGELOG.md has no Unreleased section"; exit 1; }
	@test -z "$$(git status --porcelain)" || { echo "working tree is not clean"; exit 1; }
	sed 's/^## Unreleased$$/## $(VERSION) - '"$$(date +%F)"'/' CHANGELOG.md > CHANGELOG.md.tmp \
	  && mv CHANGELOG.md.tmp CHANGELOG.md \
	  || { rm -f CHANGELOG.md.tmp; exit 1; }
	$(MAKE) tidy
	$(MAKE) check
	git add -A && git commit -q -m "Release $(VERSION)" $(if $(TRAILER),-m "$(TRAILER)")
	@scripts/release-guard.sh "$(VERSION)"
	@notes="$$(scripts/release-notes.sh $(VERSION))" || exit 1; \
	 git tag -a $(VERSION) -m "$$notes"
	git push origin --atomic HEAD $(VERSION)

clean:
	rm -rf .cache
