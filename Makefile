# ParallelScript developer commands. Upstream's own tooling (npx hereby …) is untouched.
GO ?= go
PTS_BIN := pts-bin/pts

# Upstream packages the fork edits or depends on most (docs/FORK.md); keep them green.
UPSTREAM_PACKAGES := ./internal/tspath/... ./internal/core/... ./internal/ast/... ./internal/module/... \
	./internal/compiler/... ./internal/tsoptions/... ./internal/checker/... ./internal/modulespecifiers/... \
	./internal/parser/... ./internal/scanner/... ./internal/binder/...

# A pre-existing upstream failure at the fork base (PROGRESS.md, Known issues).
UPSTREAM_KNOWN_FAILURE := TestTscCommandline/tsc/show_help_with_ExitStatus.DiagnosticsPresent_OutputsSkipped

.PHONY: build test test-diff test-stress test-upstream test-upstream-cli bench bench-http vscode

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

# The stress programs (pts/tests/stress/), each run PTS_STRESS_REPEAT times (default 1) per thread count.
test-stress:
	$(GO) test -count=1 -timeout 2h -run 'TestStress' ./pts/tests/

test-upstream:
	$(GO) test $(UPSTREAM_PACKAGES)
	$(GO) test ./internal/testrunner/ -run TestLocal

# Upstream's CLI and language-server suites: slow (several minutes).
test-upstream-cli:
	$(GO) test ./internal/execute/... ./internal/project/... ./internal/ls/... ./internal/lsp/... -skip '$(UPSTREAM_KNOWN_FAILURE)'

# ParallelScript against Node, Bun and Go (hyperfine); prints a table for docs/BENCHMARKS.md.
bench:
	pts/bench/run.sh

# HTTP servers against Node, Bun and Go (oha, requests per second; D-137).
bench-http:
	pts/bench/serve.sh

# The VS Code extension (pts/editors/vscode, D-169): its tests, then the package,
# pts/editors/vscode/dist/parallelscript.vsix (needs npm and the registry).
vscode:
	cd pts/editors/vscode && npm ci && npm test && npm run package
