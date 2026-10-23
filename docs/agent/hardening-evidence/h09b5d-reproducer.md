# H09b5d reproducer report

The `reproducer` subagent's final message (claude-opus-5-5, effort xhigh, 2026-10-23), saved verbatim by the parent. It ran in its own worktree of `fd90928f0` (`.claude/worktrees/agent-a4f68737cf2abea6e`; no tracked file changed, nothing committed). Its probes, generators, matrices, prototypes (`go build -overlay` files) and the 63 tests are in [h09b5d-reproducer-probes.tar.gz](h09b5d-reproducer-probes.tar.gz) (`INDEX.txt`). The parent's checks of its claims are in [h09b5d.md](h09b5d.md).

---


On HEAD `fd90928f0` all six committed RED tests and the m5 and m7 families still fail exactly as the earlier report recorded. I found three new failure classes that HEAD gives no warning about. All of them throw TypeErrors at run time where Node runs.

- **Body-only dependence:** the instance's IR signature is the same for both type arguments, but its body is not. This fails in every call form. Keying (B) can't fix it.
- **Direct calls with a dynamic result:** they share one instance across checker types.
- **Demand forms:** values, dispatched methods, interface and literal methods, and local arrows all look their instance up by IR signature only, so they fail the same way.

I wrote 63 run tests: 40 RED and 23 guards. Every RED fails on HEAD and every guard passes, checked with `go test` (`PTS_REQUIRE_NODE=1`). I built two throwaway prototypes, A and B, and ran everything against them and against your candidate CLI. My recommendation is A, including keyed demands. Everything below comes from real runs. HEAD's numbers are this session's, in my worktree.

## 1. Summary

| Finding | Probe or test | HEAD (ran) | Node |
|---|---|---|---|
| Six committed REDs (unskipped copies) | `probes/red6` | promises 25:57, twins 24:57, class literal 27:40, branded 15:29: PTS1001 `using a Box as a Box`. values: `Promise<string>: TypeError Expected Box<string>, got Box<any>`. past cap: `5 threw` and `6 threw Expected Box<1>, got Box<any>` | the expect blocks |
| m5 (9 cases) and m7 (98 cases) | copied cases | m5: OK 4, WRONG 5. m7: OK 21, PTS1001 69, TYPEERR 8. All 107 have the **same class and first difference** as in the H09b5 report, so H09b5c changed nothing here | — |
| C177 through more forms (m9, 120 cases) | `gen_forms.py` | OK 54, TYPEERR 66. Twins: all 6 new forms fail in both orders, and every one-call control passes. Promise pairs fail the same way in the dispatched, local-arrow and inline-arrow forms | values |
| **NEW: body-only dependence** (m10, 291 cases) | `gen_forms.py` | OK 78, TYPEERR 213. Twins: 66/66 pairs fail, 24/24 one-call controls pass. Promises: 48/66 pairs fail, controls pass. In every case the second call throws `Expected number, got string` | values |
| **NEW: direct calls with a dynamic result** | `dyn/r15–r18` | `unwrap(Promise<number>)` then `unwrap(Promise<string>)`: `string threw Expected number, got string`. `unwrap<unknown>(true)`: `Expected number, got boolean`. A generator with twins: PTS1001 `converting string to number` | values |
| Classes (b) | `cls/*` | 9 REDs: PTS1001 `using a Box as a Box` / `using a object2 as a {r: number}`, or a TypeError. 7 guards pass | values |
| Brands (c) | `brand/*` | 4 REDs: an `Id` call fails even alone. 3 guards pass | values |
| Dynamic results past 4 variants (d) | `dyn/*` | the 5th and later calls throw in the value, dispatched, local-arrow, generator, async-generator and twin forms. `any` and `unknown` calls count against the cap | values |
| Nested literal type arguments (e, 42 cases) | `nest/*` | OK 4 (literal unions in functions only), PTS1001 26, TYPEERR 12 | values |

## 2. Root causes (file:line on HEAD)

- **Direct calls share one instance.** At `generics.go:1177–1186` an instance is reused when its IR arguments and top-level literals match. A typed call (`!dynamic`) takes it whatever the checker types are. Direct calls always pass `dynamic=false` (`generics.go:689`, `:1143`). This causes the C177 direct cases, all body-only direct cases, and the shared direct dynamic results (r15–r18).
- **Nested literals are not literals.** At `generics.go:1170–1172`, `literals[]` only records a type argument that is itself a literal type. So `["a"]`, `Promise<"a">` and `{k:"a"}` are not told apart.
- **Brands are dropped.** `generics.go:1167` calls `instantiate`, and `instantiate` unbrands at `generics.go:362`. So the substitution holds `number`, not `Id`. Classes do the same at `generic_classes.go:164`. `keyPart` also unbrands (`instances.go:241`): in prototype A v1, the body-only brand cases kept failing through every demand form until I made `keyPart` write brands.
- **The dynamic cap.** Past 4 variants, `generics.go:1189` returns nil, so the call takes the erased copy.
- **Demand forms look up entries by IR signature.** Only dynamic results get a key (`instances.go:141–143`). Entries are matched on `Name`, `Signature == d.want` and `Key == d.key` (`instances.go:827`, `:1069`). At run time, `pts_instance_find` (`object.c:211`) compares the check pointer for each (Signature, Key) pair (`cgen/classes.go:958`, `:1040`). So typed demands with the same IR signature share the first entry, and with it the first instance's body.
- **Signature-differing demands fall back.** When the demand's signature differs, `tryInstance` (`instances.go:939–945`) gets back the shared instance, `fits` rejects it, and the call keeps the erased copy. That is the TypeError in the m9 forms and the values RED.
- **Classes are keyed by IR arguments only.** `generic_classes.go:170` doesn't even use literals.

## 3. Coverage findings

- **Body-only (a):** every form fails: direct function, method, module const, generic value, dispatched method, interface method, literal method, local arrow, inline arrow and class method. This holds for casts, typed locals and nested generic calls. Twin discriminant narrowing (`x.kind === "b"`, and a three-way switch) passes on HEAD and under every candidate, so those are guards. Literal keys read in a body (`o[p.key]` with `P = {key:"a"}`) also pass, because element access is resolved at run time.
- **Classes (b):** the `keyof` pattern (`Tagged<K extends keyof Shapes>` with `data: Shapes[K]`) is a realistic RED. Two design risks:
  - `r8`: storing a split class where `Tagged<keyof Shapes>` goes.
  - `r9`: `H<1>` where `H<number>` goes, with a member `T extends 1 ? any : number`.

  Both stay PTS1001 under every candidate. This is D-050's existing invariance (HEAD also gives PTS1001 for `const b: Box<number|string> = new Box<number>(1)`, probe `variance/`), now hit between instances HEAD used to share. Class body-only (`generic_class_instances_body_only_twins`) is not fixed by any rule that splits classes by their members.
- **Brands (c):** brands nested in type arguments (`Box<Id>`, `Id[]`, `Map`, `Set`, `Record`) and branded string unions pass everywhere, so they are guards.
- **Dynamic results (d):** any/unknown within the cap is a guard. Direct `Promise<1..8>` calls are a guard (they share correctly).
- **Side findings, out of scope:**
  - Calls by method name with a promise argument always take the erased copy (D-191 by design, `instances.go:611`). This confounds m9's iface/objlit promise pairs and `dyn/r3`, `r4`.
  - `x instanceof Map` in a generic body for `T = unknown` is **PTS9001** ("ir.Type is *ir.Primitive, not *ir.MapType"; probe `split2/instanceof_map_unknown_alone`). For `T = Promise<number>` it is PTS1001 `x.size`.
  - `(x as Box<unknown>)` on a `Box<number>` throws.
  - A cascade of errors follows the class PTS1001 (`using \`u\` is not supported yet`).

## 4. Prototypes and your candidate

**Prototype A**: function instances keyed by IR arguments plus `keyPart` of the type arguments, brands kept, no cap. Every demand is keyed by `instanceKey`, and in v2 `keyPart` writes brands. Classes are also keyed by their members' IR types.
**Prototype B**: IR arguments plus D-089 literals plus the deep IR identity of the instantiated signature, brands kept, no cap. Same class rule as A.

| | HEAD | parent candidate | A (v2) | B |
|---|---|---|---|---|
| Matrices and probes (620 cases), OK | 189 | 428 | **537** | 392 |
| RED/guard set (82 cases), OK | 30 | 66 | **71** | 63 |
| Cases OK on HEAD that break | — | 1 | 1 | 1 |
| Run/errors suite (overlay) | — | not run | only my REDs, plus 1 errors-test diagnostic | the same |
| `pts/lower` and `pts/driver` | — | not run | 5 IR golden pins: only the new `[key]` | all pass |

- **Your candidate** fails 5 demand-form body-only tests (values, dispatched, interface, literal and local-arrow methods): it needs keyed demands. It also fails class body-only, `keyof_members_stored` and `any_member_assignable`.
- **B** fails all twin body-only cases and the brand body-only case.
- **A** fails only class body-only and r8/r9.
- **The one case that breaks for all three:** `narrowed__arrays__decl__rev`. With its own instance, `T = Promise<number>` reaches the existing D-195/D-197 gap, PTS1001 `x.length`. It is a guard now, `generic_instances_narrowed_arrays_after_unknown`.
- My crude class split also breaks two corpus programs, `observable_streams` and `persistent_queue` (`using a Queue as a Queue`). Your narrower rule doesn't. `TestCorpusMatchesNode` was not run under any candidate.

## 5. Growth

Lines of C / build seconds / max RSS in MB. The host was noisy; medians from interleaved re-runs are in `logs/timing.log`.

**m4** since H09b5c (lines of C; seconds and MB where I measured them):

| Case | H09b5 report | HEAD now |
|---|---|---|
| esc_150x50 | 92,388 / 21.6 s / 386 MB | 9,288 / 3.0 s / 119 MB |
| methods_60 | 93,741 / 49 s / 613 MB | 12,321 / 4.0 s / 123 MB |
| decls_60 | 47,541 / 10.0 s | 8,601 / 2.1–2.3 s, all candidates |

No candidate changes m4.

**Many demand types sharing one IR type** (30 types × 10 values or functions):

| Case | HEAD | parent | A | B |
|---|---|---|---|---|
| promise via values | 4,643 (wrong output) / 1.6 s | 17,123 / 4.7 s / 133 | 17,123 / 4.7 s / 133 | 3,783 / 1.9 s / 108 |
| promise, direct | 5,048 / 2.5 s / 127 | 18,388 / 5.0 s / 142 | 18,388 / 5.3 s / 142 | 5,048 / 2.3 s |
| twins via values | 667 / 0.9 s / 100 | 667 | 4,495 / 3.1 s / 117 | 667 |
| twins, direct | 508 | 3,118 | 3,118 | 508 |
| literals via values | 602 | 602 | 4,430 | 602 |

- **Polymorphic recursion through one IR type:** `keyPart` is not bounded. Past its depth it writes a dynamic value by its checker id. So `nest<T>` calling `nest<Promise<T>>` goes from 1 instance to **65** (170 → 4,684 lines) in your candidate and in A, and a template-literal twin recursion goes from 95 → 929 lines (B: 660 and 95). Both still print Node's output, because past depth 64 the call takes the erased copy.
- **Across all 1,073 run and corpus programs:** HEAD 1,595,159 lines; your candidate +0.5% (+7,916, of which 5,348 comes from my two recursion guards); A +0.9%; B +0.04%.

## 6. Tests written (worktree `pts/tests/run/`, 63 files, untracked)

Each file's header says what its RED or guard is for. The HEAD failure listed is the one that `go test` confirmed.

**Body-only.** All throw `Expected number, got string` except the brand test:
- REDs: `generic_instances_body_only_{twins,promises,nested_call}`, `generic_methods_body_only_promises`, `generic_values_body_only_twins`, `generic_{dispatched,interface,literal}_methods_body_only_twins`, `generic_local_arrows_body_only_twins`, `generic_class_instances_body_only_twins`, `generic_instances_branded_body_only` (`Expected {count: number}, got Object`).

**Classes and the m9 forms:**
- `generic_{dispatched,interface,literal}_methods_same_ir_type_twins` and `generic_inline_arrows_same_ir_type_twins`: TypeError `Expected Box<string>, got Box<string | number>`.
- `generic_local_arrows_same_ir_type_promises`: TypeError `Expected Box<string>, got Box<any>`.
- PTS1001: `generic_class_instances_{keyof_members, keyof_members_stored, twin_type_arguments, promise_type_arguments, literal_generic_method, split_behaviours, branded_type_arguments, union_literal_type_arguments}`, `generic_subclass_instances_literal_type_arguments`.
- `generic_class_instances_any_member_assignable`: TypeError.

**Brands and nested literals:**
- `generic_values_branded_type_arguments`: TypeError.
- `generic_instances_two_brands`: PTS1001.
- `generic_instances_tuple_literal_type_arguments`: PTS1001.
- `generic_values_promise_literal_type_arguments`: TypeError.

**Dynamic results:**
- WRONG (`N threw …`): `generic_dynamic_variants_{past_cap_dispatched, past_cap_local_arrow, past_cap_async_generators, past_cap_generators, past_cap_twins, any_after_literals, mixed_past_cap}`, `generic_instances_dynamic_results_{same_ir_type,unknown}`, `generic_methods_dynamic_results_same_ir_type`.
- PTS1001: `generic_generators_same_ir_type_twins`.

**Guards (23):**
- Class literal arguments: `generic_class_instances_literal_{arguments_tagged, to_base, instanceof, statics, generic_methods, mixed_array}`, `generic_subclass_instances_literal_shared`.
- Brands: `generic_instances_nested_brands`, `generic_values_branded_share_primitive`, `generic_instances_branded_string_union`.
- Dynamic results: `generic_dynamic_variants_any_unknown_within_cap`, `generic_dynamic_variants_literal_generators`, `generic_instances_dynamic_results_literal_promises`, `generic_values_dynamic_results_promise_number_string`.
- Literal unions: `generic_instances_literal_union_type_arguments`.
- Twin narrowing: `generic_{instances,values,class_instances}_twin_discriminant_narrowing`, `generic_instances_twin_switch_narrowing`.
- Recursion: `generic_polymorphic_recursion_one_ir_{type,shape}`.
- Split risk: `generic_instances_narrowed_arrays_after_unknown`. Your candidate, A and B all give PTS1001 here.

## 7. Recommendation

**Use keying A, and key every typed demand too, not only dynamic results.** It is the only keying that fixes body-only dependence. Under B those cases are not compile errors: they compile and then throw TypeErrors at run time where Node runs, in 162 of the 620 cases. Concretely:

- **Make `keyPart` brand-aware**, for both the instance key and `instanceKey`.
- **Drop `madeFor` and `maxDynamicVariants`.**
- **Classes:** your narrower split rule (only members whose declared types compute types from T) is safer than splitting on all members' IR types: mine broke two corpus programs and yours didn't. Accept class body-only, r8 and r9 as known issues (D-050's invariance), or design layout-sharing class splits later.

**Risks:**
1. **Growth:** 3.6–7× C lines and about 3× build time when many checker types share one IR type, and unbounded `keyPart` lets polymorphic recursion run to depth 64. Bound `keyPart` for dynamic values: past `maxKeyDepth`, write the IR type, not the checker id. Possibly also merge instances whose lowered IR is identical (suspected to recover B's size; not prototyped).
2. **Dead regions:** separate instances reach D-195/D-197's gaps for dynamic type arguments (`narrowed_arrays_after_unknown`).
3. **Golden pins:** keyed demands change the IR golden output of 5 `pts/lower` tests (only the printed `[key]`).
4. **Not run:** the corpus suite under any candidate, and the unit and full suites under your candidate.

## Deliverables

Everything is in `/tmp/h09b5d/reproducer/h09b5d-reproducer-probes.tar.gz` (sha256 `1526fcc4…`, 1,768 entries, `INDEX.txt` inside; no binaries). It holds the probes with their `.node`/`.head` outputs, the generators and runners, every results table, the 63 tests (`red/`), the overlay prototypes and the logs. `/tmp/h09b5d/reproducer/DONE` is touched.

Worktree: `/home/bryan/projects/parallelscript/.claude/worktrees/agent-a4f68737cf2abea6e`. Nothing is committed, and the 63 untracked tests are the only change.