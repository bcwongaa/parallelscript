# H09b5b resumed reproducer report

Date: 2026-10-21. Agent `ab5a39deedb854bdc`, named `reproducer`, isolated worktree. The report below is the child’s retained final text, not independent acceptance of the parent’s fixes. Parent replay and dispositions are in [h09b5b-resume-review.md](h09b5b-resume-review.md). The archive was copied to [h09b5b-resume-reproducer-probes.tar.gz](h09b5b-resume-reproducer-probes.tar.gz); paths inside the report identify the original worktree.

---

## Verified remaining H09b5b/C176 gap

**Four wrong-output cases reproduce at checkpoint 9132cb9d1 and base 042cd9610. These are remaining defects, not regressions introduced by the checkpoint.**

| Case | Checkpoint behavior |
|---|---|
| One call node across generic function instances | Drops working `number` pairing alongside failing `Dog[]` pairing |
| One call node across generic class instances | Same collision in `Relay<X>.call` |
| Relaxed-cast failure | Drops working `Good` pairing alongside failing `Base` pairing |
| Same method declaration, different generic owners | Drops working `Holder<number>.make<number>` alongside failing `Holder<Dog[]>.make<number>` |

Representative checkpoint output:

```text
weird 0
same call number TypeError Expected Box<number>, got Box<any>
same call dogs 1
```

Node instead prints `same call number 6`.

For the fourth case, checkpoint and base print:

```text
outer weird 0
outer number TypeError Expected Box<number>, got Box<any>
kept dogs 1
```

Node prints `outer number 6`.

Node, successful `emit-c`, build, and program commands all exit **0**; the harness exits **1** because stdout is wrong. Both the historical snapshot CLI and my independently built checkpoint CLI confirm the original three failures.

## Stable-key recommendation, experimentally checked

Retain existing call/name/value/level identity, adding:

1. **Demand identity:** serialize instantiated checker type IDs for resolved-signature type arguments, parameters, and result while the demand’s actual substitution is active. Cache this at demand creation.
2. **Target identity:** serialize the target generic’s outer parameter-to-argument bindings, including erased parameters. Demand identity alone does not distinguish the fourth case.

Observation-only overlay tests both exited **0**:

- Demand IDs remain stable across repeated `ProgramWith` calls, selective-narrowing relowerings, and fallback levels.
- Target-owner binding IDs remain stable across repeated and narrowed lowerings.
- Raw signature identity conflates differently instantiated demands.
- IR and substitution pointers change between lowerings.

This recommendation is scoped to relowerings of the **same frontend program/checker**, not persistence across fresh program loads. Do not reuse `instanceKey` or `keyPart`: they contain fresh IR pointers/order-based labels.

## Clean checks

- Program-owned class failure remains **PTS1001** at all three fallback levels, both plain and selectively narrowed APIs: **6/6 clean compile errors**, helper exits **0**.
- Concrete generic class/method outer-cycle control matches Node at checkpoint; base loses the unrelated victim instance.
- Constant-key trial/site-precedence control matches Node on both versions.
- Every successfully built runtime probe has byte-identical emitted and actually compiled C.

One initial own-CLI build invocation failed with exit **1** because the isolated runtime’s Ryu submodule was absent. Rerunning that CLI with the initialized snapshot runtime succeeded; both attempts are retained.

## Evidence

Archive, excluding binaries:

`/home/bryan/projects/parallelscript/.claude/worktrees/agent-ab5a39deedb854bdc/probes/h09b5b-independent-audit.tar.gz`

SHA256:

```text
5c2036970f6e15c32504bb6ba87948ab014d61f7dacabd5067a0dc02b37a7c75
```

Exact command/status ledgers:

- `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ab5a39deedb854bdc/probes/h09b5b-audit/extra-commands.json`
- `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ab5a39deedb854bdc/probes/h09b5b-audit/checkpoint-v-base/commands.jsonl`
- `/home/bryan/projects/parallelscript/.claude/worktrees/agent-ab5a39deedb854bdc/probes/h09b5b-audit/own-cli/commands.jsonl`

Archive validation exited **0**: 190 members, no unsafe paths or ELF binaries. No production edits, task ticks, commits, pushes, or shared-tree writes. No heavy jobs remain active.

SESSION_RESULT: done=H09b5b-audit next=H09b5b green=no
