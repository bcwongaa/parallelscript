# ParallelScript developer commands. Upstream's own tooling (npx hereby …) is untouched.
GO ?= go
PTS_BIN := pts-bin/pts

# Upstream packages the fork edits or depends on most (docs/FORK.md); keep them green.
UPSTREAM_PACKAGES := ./internal/tspath/... ./internal/core/... ./internal/ast/... ./internal/module/... \
	./internal/compiler/... ./internal/tsoptions/... ./internal/checker/... ./internal/modulespecifiers/... \
	./internal/parser/... ./internal/scanner/... ./internal/binder/...

.PHONY: build test test-diff test-upstream bench

# The compiler, at pts-bin/pts (it finds the runtime sources from there).
build:
	$(GO) build -o $(PTS_BIN) ./cmd/pts

# Every ParallelScript test: Go unit tests, runtime C tests, the corpus in pts/tests
# and, if node is on PATH, the differential tests against Node.
test:
	$(GO) test ./pts/... ./cmd/pts/

# Only the differential tests; fails if node is missing.
test-diff:
	PTS_REQUIRE_NODE=1 $(GO) test -count=1 -run 'TestRunMatchesNode' ./pts/tests/

test-upstream:
	$(GO) test $(UPSTREAM_PACKAGES)
	$(GO) test ./internal/testrunner/ -run TestLocal

bench:
	@if [ -x pts/bench/run.sh ]; then pts/bench/run.sh; else echo "No benchmarks yet: pts/bench/run.sh arrives with M1.5."; fi
