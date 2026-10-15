# H02 independent review and dispositions

Reviewer: the named `opus-reviewer` subagent (claude-opus-5-5, effort xhigh), read-only, in two rounds on the parent's uncommitted tree; scratch `/tmp/pts-h02-review` (probes `p/` for round 1 and `p2/` for round 2, outputs `out/<name>/{node,new,base}.*`). Every probe ran under Node, the current build and the base (`389baf63d` compiler and runtime, `PTS_HOME` = the reproducer's worktree). The parent re-ran all 105 probes itself against each later build (`/tmp/pts-h02/rv`) and checked each claim below before acting on it. Retained: [h02-review-probes.tar.gz](h02-review-probes.tar.gz) holds the probe sources (`p/`, `p2/`), the reviewer's `bin/cmp.sh`, and the final build's and Node's stdout, stderr and exit codes per probe (`final-outputs/`), with the 44 that differ from Node listed in `final-differing.txt`.

## Round 1 (the first fix: unchecked narrowing)

| Finding | Severity | Disposition |
|---|---|---|
| `narrowedAssignment` converted `&&=`'s value without a check; a module-scope `let x: A \| B;` without a value, a static or `!` field, or a field read before the constructor sets it (D-141) holds undefined outside the type, so `(label &&= "hi").length` segfaulted (exit 139) and `(n &&= 5)` printed NaN | BLOCKER | **Fixed**: the value is converted with a check (`checkedConversion`, as `narrowed` does for properties, D-089/D-140). Now Node's output, or a checked TypeError where Node goes on with undefined (D-141's documented gap). |
| `\|\|=` on a dynamic place tsc types (keyed element `Record<Status, number>[k]`, `keyof` index, type-guarded `unknown`) built `ir.Logical` from a narrowed `number` read and a `Value` store, printing a double's bits (4617878467915022000); `&&=` and loose locals' `\|\|=` did so on the base already | BLOCKER | **Fixed**: the stored value is converted to the read's type (checked). |
| Keyed `??=`/`\|\|=` used as an array panicked in cgen (`arrays.go:12`) | SHOULD-FIX | **Fixed** with the same conversion. Plain `=` (same panic on the base) → C26, H04, skipped test `run/keyed_assign_value.pts`. |
| `(cache ??= new Map()).get/set(…)` panicked (`maps.go:244`) on both compilers | SHOULD-FIX | **Fixed**: where tsc's type isn't the place's or a narrowing of it, the place's type (without null/undefined when the right side can't be either). |
| `await`/`yield` on the right side loses the short-circuit (typed places on the base too) | SHOULD-FIX | **Recorded**: C25, H08; skipped test `run/logical_assign_suspensions.pts` with Node's output; PROGRESS known issue. |
| Test gaps (`&&=` narrowing, keyed/guarded values, typed union idioms, C24 through a setter/tuple/union field, `Record<string, string \| undefined>` `+=`) | SHOULD-FIX | **Added**: `run/logical_assign_typed_values.pts`; sections in `dynamic_write_value.pts` and `record_any_entries.pts`. |
| `any` keys are converted once, before the right side; V8 converts at each get/set (a key with a side-effecting `toString`) | NIT | **Documented** boundary (h02.md); symbols aren't values in PTS. |
| Inaccurate test headers; comment and signature style | NIT | **Fixed**. |

## Round 2 (the checked version)

The reviewer confirmed both round-1 blockers resolved (`let_unset_and`, `static_unset_and`, `definite_field_and`, `keyed_or_garbage`, `keyed_or_value`, `keyed_and_value`, `guarded_dynamic_or`, `guarded_dynamic_and`, `loose_or_empty`, `keyed_e`, `keyed_d` match Node) and all ten test files (expect = Node, GREEN on the build, RED on the base).

| Finding | Severity | Disposition |
|---|---|---|
| The check ran on values nothing uses: the statement `label &&= "hi";` on an unset union place threw where Node and the base go on (`p2/statement_forms2.pts`) | BLOCKER | **Fixed**: `isDiscarded` (expression statement, comma, `void`, `for` update) skips the conversion; `\|\|=`/`&&=` there test the unnarrowed read, so the stored value isn't checked either (`p2/guarded_any_store.pts`, the reviewer's NIT, now matches Node). Regression lines in `logical_assign_typed_values.pts`. |
| On keyed elements, an untyped `new Array()`/`new Map()` on the right was converted to tsc's `any[]`/`Map<any, any>`: a copy of the stored typed array (pushes lost) or a TypeError (`p2/keyed_any_array.pts`, `keyed_map_lazy.pts`) | BLOCKER | **Fixed**: `logicalAssignmentType` uses the place's type there, for dynamic places as for unions. Regression lines added. `p2/keyed_any_array2.pts` line 8 (`const got = (existing[k] ??= new Array())`, `got` an `any[]` to tsc) is now an honest PTS1001 (D-094: a typed array used as `any[]` where it may be kept), as `array_any_fallback`/`map_any_members2` were on the round-2 build. |
| C26 deferral acceptable, but needs a skipped test | SHOULD-FIX | **Done**: `run/keyed_assign_value.pts`. |
| Remaining honest PTS1001s around untyped `new Map()` (`forEach`, a `Map<any, any>` parameter, `.set("a", "str")` on a `Map<string, number>` place, several object members) | NIT | **Kept** as honest diagnostics (h02.md boundaries). |
| A loose local holding undefined throws on `\|\|=` | NIT, pre-existing | H11 owns loose values. |

## Parent's own follow-up checks

- After the round-2 fix, `p2/any_array_rhs.pts` line 14 (`(guarded.other ??= new Array()).push(7)` on a type-guarded JSON object) loses the push where the round-2 build kept it. That build matched Node only because tsc's `any[]` happened to be the JSON array's representation. A plain `guarded.list!.push(7)` loses its push too, on this build and the base (scratch `j1.pts`: `{ list: [ 1 ], other: [ 2 ], more: [] }`, Node `{ list: [ 1, 7 ], other: [ 2, 8 ], more: [ 9 ] }`): SPEC §8's documented copy of dynamic data read as a typed array. Kept, documented in D-176.
- 61 of the 105 probes match Node on the final build; each of the other 44 is classified in h02.md ("Review probes").
