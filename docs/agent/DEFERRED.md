# Deferred hardening scope

Scope moved out of the ordered plan under the owner's 2026-10-22 time-box rule: work that is precision,
performance, generated-code size or other refinement where programs already behave correctly. Nothing
here is done; each entry names the later task that owns it and must be listed again in HARDENING_DONE.

## H09b5c
- **What remains:** Retain parameter/returned containers, default/rest/terminal-value and other destructured containers, numeric/computed keys and explicit pattern-owner annotations, nonliteral nested origins, broader caller/return flow beyond accepted children where provable, remaining receiver/source precision and necessary m4 measurements. No scope is dropped; uncertain flows retain compatible instances.
- **Why deferred:** after 14 nested slices (H09b5c1 … H09b5c2b2b2b2b2b2b2a, D-201–D-215, all committed with
  green local suites) the remainder kept being re-split into ever-narrower flow-precision cases. Every
  uncertain flow already keeps a compatible instance, so the remaining cost is C size and build time
  (C172: esc_150x50 92,388 lines / 21.6 s; methods_60 93,741 lines / 49 s), not wrong results.
- **Owner:** H25 (escape analysis / flow) and H27 (code generation), with the m4 matrix as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h09b5c*.md, RED test `driver/speculative_instances_test.go`.

## H09b5d
- **What remains (C266):** typed calls through generic values and methods are keyed by their literal-precise checker types (D-216), so a value's table gets an entry (wrapper, adapter, check object) per key even where every key's instance is one function, and `pts_instance_find` scans a value's entries linearly on every call. Make a declaration that computes no types from its type parameters register one unkeyed entry, found after a keyed lookup misses (runtime and C emitter), or give `pts_instance_find` a per-site cache as methods have.
- **Why deferred:** programs behave correctly; it is generated-code size and call cost (review round 2's F4: 200 action types through five values 853 → 7,221 lines of C, one value 629 → 5,405; a hot call whose entry is the 201st about 4× slower). Keys can't be coarser: a value whose type computes nothing but whose body does needs them (`run/generic_values_body_only_twins.pts`).
- **Owner:** H27 (code generation), with the review's `g02`–`g09` probes as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h09b5d.md, h09b5d-review.md (round 2), `h09b5d-review2-probes.tar.gz` (`p/g07_values_typed_200.pts`, `g08_value_one_200.pts`, `g09_value_lookup_hot.pts`).

## H10a
- **What remains (R2-F6):** a member of a reference narrowed by `instanceof` that tsc types with any (`c.items` an `any[]` for MapLRU<string, number>'s `string[]`) is read, written and called as a dynamic value (D-218), so a hot loop over `c.items[i]` and calls with callback arguments go through the runtime's dynamic lookups. Keep such a member typed where it is only the object of reads whose results tsc's types accept (element reads, `.length`, typed methods), boxing the result instead.
- **Why deferred:** programs behave correctly (as Node, or an honest checked TypeError); it is run time: review round 2's `q02` sums `c.items[i]` in about 1.1 s against 0.22 s through a typed parameter (Node 64 ms, a loaded host), and `q16`'s 2M dynamic calls take 280 ms against 10 ms typed.
- **Owner:** H27 (numeric and array code generation), with `q02_hot_loop.pts` and `q16_hot_dynamic_calls.pts` as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h10a.md, h10a-review.md (round 2), `h10a-review2-probes.tar.gz`.

## H10b
- **What remains (D-219's cost):** a place tsc types `C<any, …>` holds any of C's instances as a dynamic value, so reads, writes and calls through it are found at run time, also where the place provably holds C's instance for any (`const hot = new Box<any>(0)`, which the base lowered as a typed class). Keep such a place typed where every value it holds is made by `new C<any>(…)` (or another proof of the exact class), as H09b5c's exact constructed receivers do, boxing it where it flows into other C<any, …> places.
- **Why deferred:** programs behave correctly (as Node, or D-070's checked TypeError for a write of another type); it is run time: 1M iterations of a write, a read and a method call through `const hot = new Box<any>(0)` run 140.8M instructions on the base and 761.8M with H10b (callgrind, 5.4×).
- **Owner:** H27 (numeric and array code generation), with the probe `hot.pts` in `h10b-parent-probes.tar.gz` as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h10b.md.

## H12
- **What remains (D-222's cost):** an object pattern's element with a default over a hash-map object of numbers or booleans (`const { a = 0, b = 0 } = rec` for a `Record<string, number>`) reads the entry with `MapGet`, whose `number | undefined` union is boxed and tested, so that a missing key takes the default (C334); the base read it with `MapGetChecked` as a number and threw RangeError for a missing key.
- **Why deferred:** programs behave correctly (as Node); it is run time: the probe's loop (two such elements, keys present) costs 97.0M instructions on the base and 119.4M with H12 (callgrind, 1.23×). A typed get-or-undefined (`pts_map_get` into the value's own type and a found flag, as `?? fallback` over a record reads it) would read the number without boxing.
- **Owner:** H27 (numeric and array code generation), with `cost/record_default_present.pts` in `h12-parent-probes.tar.gz` as the measurement.
- **Evidence:** docs/agent/hardening-evidence/h12.md, D-222's Costs.
