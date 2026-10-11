# ParallelScript developer commands. Upstream's own tooling (npx hereby …) is untouched.
GO ?= go
PTS_BIN := pts-bin/pts

# Upstream packages the fork edits or depends on most (docs/FORK.md); keep them green.
UPSTREAM_PACKAGES := ./internal/tspath/... ./internal/core/... ./internal/ast/... ./internal/module/... \
	./internal/compiler/... ./internal/tsoptions/... ./internal/checker/... ./internal/modulespecifiers/... \
	./internal/parser/... ./internal/scanner/... ./internal/binder/...

# A pre-existing upstream failure at the fork base (PROGRESS.md, Known issues).
UPSTREAM_KNOWN_FAILURE := TestTscCommandline/tsc/show_help_with_ExitStatus.DiagnosticsPresent_OutputsSkipped

.PHONY: build test test-diff test-upstream test-upstream-cli bench

# The compiler, at pts-bin/pts (it finds the runtime sources from there).
build:
	$(GO) build -o $(PTS_BIN) ./cmd/pts

# Every ParallelScript test: Go unit tests, runtime C tests, the corpus in pts/tests
# and, if node is on PATH, the differential tests against Node.
test:
	$(GO) test ./pts/... ./cmd/pts/

# Only the differential tests (every test named *MatchesNode); fails if node is missing.
test-diff:
	PTS_REQUIRE_NODE=1 $(GO) test -count=1 -run 'MatchesNode' ./pts/tests/

test-upstream:
	$(GO) test $(UPSTREAM_PACKAGES)
	$(GO) test ./internal/testrunner/ -run TestLocal

# Upstream's CLI and language-server suites: slow (several minutes).
test-upstream-cli:
	$(GO) test ./internal/execute/... ./internal/project/... ./internal/ls/... ./internal/lsp/... -skip '$(UPSTREAM_KNOWN_FAILURE)'

# ParallelScript against Node, Bun and Go (hyperfine); prints a table for docs/BENCHMARKS.md.
bench:
	pts/bench/run.sh
