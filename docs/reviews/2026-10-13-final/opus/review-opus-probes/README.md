# Opus review: programs and probes (2026-10-13, snapshot f2323ccf4)

Everything `REVIEW-opus.md` cites. Build the compiler first (`make build` at the repo root).

- `programs/csvq/`: a CSV query CLI (`csvq.pts`, data `sales.csv`, the 15 command lines in `cmds.txt`) and a `pts test` example (`test/`).
  For Node: `sed 's#"pts:#"node:#' csvq.pts > csvq.ts`, then `node --experimental-transform-types csvq.ts sales.csv …`.
- `programs/todo_service/`: a JSON todo service on every loop plus a `fetch` client. `todo_service` runs the client; `todo_service --serve PORT` serves.
  `tools/slowloris.py PORT N` and `tools/rst.py PORT` are the robustness tests.
- `programs/analytics/`: a parallel analytics job (five parallel strategies checked against a sequential one, `join` sort, k-means).
  `tools/tonode.sh analytics.pts out.ts` rewrites it for Node with sequential stand-ins from `tools/nodeshim/`.
- `probes/`: targeted probes. `tools/cmp.sh probes/pNN_x.pts [threads]` builds, runs and diffs against Node;
  `NONODE=1` for the ParallelScript-only probes (p03, p04, p11, p13–p16, p27). `probes/lsp/lsp_smoke.py PTS FILE` is the LSP smoke test.

The findings' repros: p01/p28 (destructured `split` into an optional property), p08b/p08d/p08f (compiler panic),
p18/p23 (`flat`/`flatMap`), p24 (`sort` with undefined/null), p21 (`const o: any = {}`), p04/p15/p16 (race and GC runs, no crash expected).
