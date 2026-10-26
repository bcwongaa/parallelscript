# H36 recovery: finite reproducer report

2026-10-26. Parent-assembled record of named `reproducer` `a97b28fd6488e324a`'s delivered report. Isolated worktree based at `f0a3cf56d`, recovered H36 port, inherited `gpt-6.1-sol`. This is test design/reproduction, **not independent port acceptance**; the refused review assignment was not retried or transferred to this child.

## Execution

Common child command prefix (from the isolated worktree):

```
GOMAXPROCS=1 PTS_HOME=<isolated-worktree> go test -count=1 -json -p 1 -parallel 1 -timeout 10m
```

| Scope / selector | Exit / result | Raw stream in archive |
|---|---|---|
| `^TestMemoryUsageKeepsHeapCountersFromOneSnapshot$`, recovered port | 1; `true false true`, `incoherent heap 4259840 -12533760`; shim grew the real collector by 12648448 bytes | `memory-recovered-red.jsonl` |
| Same regression with parent's approved three safe-snapshot files copied into child tree | 0 | `memory-green.jsonl` |
| Exact-base emitter overlay, `^TestLineDirectives(ResolveSourceFileAndDirectoryLinks\|KeepUnresolvableSourceNames)$` | 1; all three symlink cases fail, all three unresolved-name controls pass | `cgen-base-red.jsonl` |
| Exact-base target overlay, `^TestMacOS(TargetPropagatesToEveryBuildStep\|AndLinuxUseSeparateArchivesWithTheSameCompiler)$` | 1; both reach `unknown target "aarch64-macos": pts builds for x86_64-linux or aarch64-linux` | `toolchain-base-red-behavior.jsonl` |
| Recovered emitter and target/cache tests | 0; four top-level tests, six cgen subtests pass | `platform-seams-green-v2.jsonl` |
| `^(TestRun\|TestRunMatchesNode)$/(process_memory_usage\|fs_native_flags)$`, `PTS_REQUIRE_NODE=1` | 0; both strict Node comparisons pass; ordinary memory fixed output passes; platform-dependent fs fixed output intentionally skipped | `fs-memory-node-green-v2.jsonl` |
| `^TestProcessNamesItsTargetAndResolvesItsExecutableLink$` | 0; exact platform/arch, aliases and executable-link resolution | `process-target-green.jsonl` |
| `git diff --check` | 0 | reported by child |

All listed final command-level stderr files are empty. The collector-growth diagnostic is captured in the test JSON. The single-snapshot production fix belongs to the parent, not to this child. Parent independently executed the memory RED/GREEN and all integrated permanent GREEN cases; see [parent recovery evidence](h36-resume.md).

## Incorrect new-test assumptions retained separately

- `toolchain-base-red.jsonl` and `platform-seams-green.jsonl`: a string incorrectly passed as the fourth `assert.DeepEqual` argument, which accepts comparison options. Removing only that unsupported message argument permits execution of the behavioral controls above.
- `fs-memory-node-green.jsonl`: the first fixture used numeric flags in read/write option objects, whose existing type is `flag?: string`. Original input `h36_base/fs_numeric_unsupported.pts` is preserved as test-design evidence, not an H36 runtime RED. Parent's suggested numeric `openSync`/`promises.open` alternative also does not exist (SPEC explicitly excludes file descriptors). No production API widening: final fixture compares native numeric constants and all 16 supported write / six read string aliases, sync and promises.
- An initial `taskset` wrapper invocation was denied before execution; the actual tests used the ordinary authorized one-job command above, without an escalation or workaround for the denied call.

## Historical Mac inspection

No local Mac execution. The child inspected scratch `35089901ea49e5c697fe47a998b93ad3abc9ef56`'s existing r5 artifacts: **6,042 test pass / 416 test skip / 0 fail**, ten packages passing; stderr 87 bytes (two Go module-download notices). Recovered production differs only in comments in four scoped source files; Darwin config headers match the historical blobs. Derived data: `mac-history.json`, `mac-r1-fs-path-output.txt`.

## Retained inputs

[h36-resume-reproducer-probes.tar.gz](h36-resume-reproducer-probes.tar.gz): 41 regular text files; SHA256 `871b8b9016773e3e470bbafcb71bb3e48cc1b7ff741a5462892a4bcc678f249f`. Contains nine permanent test candidates, raw finite RED/GREEN/setup streams, source hashes, derived historical evidence and exact-base overlay inputs. No binaries, symlinks or key material. Overlay mappings contain the original isolated worktree's absolute paths; remap before replay. `h36_base` is evidence only and is **not integrated as permanent tests**.

The child did not write the parent's tree, commit, push, tick tasks, query CI, run a full suite or replace the reviewer. Its one-job budget was released before the parent's final four-job suite.
