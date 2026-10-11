# ParallelScript: agent instructions

You are building **ParallelScript**, which is TypeScript compiled to native binaries, with a GC and real parallelism, inside a fork of typescript-go (TypeScript 7, pinned at `typescript/v7.0.2`).
You run as one session in a long chain of autonomous sessions. **The repository is the only memory that survives between sessions.**

## Read first, every session, in this order
1. `PROGRESS.md`: current milestone and task, known issues, build and test commands. Bootstrap it from `docs/MASTER_PLAN.md` if it doesn't exist yet.
2. `docs/MASTER_PLAN.md`: the ordered task list. You work on the first unchecked task.
3. `docs/DECISIONS.md`: everything already decided. Until it exists, read `docs/agent/PREDECISIONS.md`.
4. The parts of `docs/SPEC.md` relevant to the task. `docs/BRIEF.md` is the owner's original mission brief and the source of truth behind the spec.
5. `docs/agent/OWNER_ANSWERS.md`, if present: answers the owner has given to earlier questions.

## Owner overrides of the brief (these win over docs/BRIEF.md)
- **The source file extension is `.pts`**, as in the brief. The owner briefly switched to `.ps` and then reverted (D-004). `.ps` is not a ParallelScript extension; never reintroduce it. Older commits and log entries that mention `.ps` are history.
- **The repo is `github.com/bcwongaa/parallelscript`** (remote `origin`). `upstream` is microsoft/typescript-go. Never push to upstream.
- The owner wants maximum autonomy. Decide things yourself, log them in `docs/DECISIONS.md`, and keep going (see "Asking the owner").

## Working rules (from the brief, condensed)
- Work in vertical slices. A feature is done only when it has tests and they pass.
- Commit small and often **directly on `main`** and push after every green step (`git push origin main`). The brief's "keep main green" rule replaces the PR flow in the LLM-dev-guides suite. Never force-push. Tag finished milestones (`m0`, `m1`, …) and push the tags.
- Never edit a test to make it pass, unless the test itself is wrong; then say why in the commit message.
- Never reopen locked decisions. If you think one is wrong, write `docs/proposals/NNN-title.md`, notify the owner, and keep working on other things.
- **Time-box rabbit holes.** After about five serious attempts at one bug: record it under "Known issues" in `PROGRESS.md`, add a skipped test that reproduces it, and move on.
- Upstream edits are minimal, marked `// PTS:`, and listed in `docs/FORK.md`. New code goes in `cmd/pts/` and `pts/`. Keep the relevant upstream Go tests green: `go test ./internal/<pkg>/...` for every package you touch.
- Generated C must be readable and carry `#line` directives.
- Code quality: follow `~/projects/LLM-dev-guides/guides/code-style/RULES.md` and `guides/testing/RULES.md` when writing code. Read them once per session; don't load the rest of that suite. Where the suite conflicts with this file or the brief, this file wins.

## Session protocol
1. **Start.** Run `git status` and `git log --oneline -5`. A dirty tree means the previous session was cut off mid-task (usage limit or timeout). Read its diff and decide: finish and commit it, or `git stash` it with a message recorded in `PROGRESS.md`. Never silently discard it.
2. **Baseline.** Run the build and test commands from `PROGRESS.md` and note the pass/fail counts.
3. **Work.** Take the first unchecked task in `docs/MASTER_PLAN.md` and complete it end to end. Use subagents (the Task tool) for parallelisable side work such as research, test corpus writing and reviews. You remain responsible for verifying their output.
4. **Task too big for one session?** Split it in `MASTER_PLAN.md` into `X.Ya`, `X.Yb`, … without dropping scope, finish the first part properly, and log the split in `DECISIONS.md`.
5. **After each task:**
   - tick it in `MASTER_PLAN.md` with the short commit hash
   - update `PROGRESS.md` (done, next, known issues, commands)
   - commit and push
6. **Milestone done** (its last task ticked): follow the milestone checklist in `MASTER_PLAN.md`.
7. **Continue or stop.** If context is still under about 60% and time allows, continue with the next task. Otherwise stop cleanly, with everything committed and pushed.
8. **End** your final message with one line: `SESSION_RESULT: done=<task ids> next=<task id> green=<yes|no>`.
9. **All of M0–M7 done?** Create `docs/agent/DONE` containing a short summary, commit, push, send the final Telegram report, and stop.

## Asking the owner: don't
The owner said not to ask them anything until the whole project is done. Decide every open question yourself, using the spec, the brief's §3 tie-breakers and `docs/agent/PREDECISIONS.md`. Log each one in `docs/DECISIONS.md` and keep going. If a decision is big enough that the owner should review it later, also list it in `docs/agent/REVIEW_LATER.md`. That file is for review after DONE; never wait on it.

## Telegram (status only, never questions)
Send a message at each finished milestone, and for a proposal (a heads-up only; don't wait for a reply).
```sh
hermes send -t telegram:-1004441160190:228 -f /path/to/msg.txt
```
Keep messages under 3500 characters and in plain text or simple Markdown. A milestone message covers: what now works, a short code sample that shows it off, and benchmark numbers if there are any.

## Environment
- Go 1.26 is at `~/.local/go/bin`, Node LTS at `~/.local/node/bin` (use this one for differential tests), Bun at `~/.bun/bin`. The session driver puts all of them on PATH.
- Also installed: clang 18, cmake, ninja, autotools, gdb, lldb, valgrind, hyperfine, oha and wrk.
- The machine is x86_64 Linux Mint 22.2 (Ubuntu noble) with 8 cores and 15 GB of RAM. Don't run more than about 6 parallel heavy jobs, because the host also runs other services.
- No sudo. If you truly need a system package, add it to "Needs from owner" in `PROGRESS.md` and work around it, for example by vendoring and building from source.
- Never read or print `~/.hermes/.env`, `~/.hermes/auth.json`, `~/.cli-proxy-api/` or `~/.ssh/`. Never touch `~/.hermes` apart from the `hermes send` command above.
