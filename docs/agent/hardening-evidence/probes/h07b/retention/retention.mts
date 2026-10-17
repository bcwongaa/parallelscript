// H07b retention (information only): BATCHES batches of N AbortSignal.any([longLived]) results,
// each dropped at once; prints the resident set size after each batch. A follower its source
// holds strongly keeps growing the RSS batch after batch; one held weakly doesn't (Node).
import { readFileSync } from "node:fs";

const N = Number(process.env.N ?? "1000000");
const BATCHES = Number(process.env.BATCHES ?? "3");

function rssMiB(): string {
  const status = readFileSync("/proc/self/status", "utf8");
  const line = status.split("\n").find((l) => l.startsWith("VmRSS:")) ?? "VmRSS: 0 kB";
  return (Number(line.replace(/[^0-9]/g, "")) / 1024).toFixed(1);
}

const gc = (globalThis as any).gc as (() => void) | undefined;
const longLived = new AbortController();
console.log(`start: rss ${rssMiB()} MiB`);
for (let batch = 1; batch <= BATCHES; batch++) {
  const t0 = performance.now();
  for (let i = 0; i < N; i++) {
    AbortSignal.any([longLived.signal]);
  }
  gc?.();
  console.log(`batch ${batch} (${N} dropped followers, ${(performance.now() - t0).toFixed(0)} ms): rss ${rssMiB()} MiB`);
}
// Controls: the same number of plain controllers' signals dropped, and kept.
{
  for (let i = 0; i < N; i++) {
    new AbortController();
  }
  console.log(`control: ${N} controllers dropped: rss ${rssMiB()} MiB`);
  const kept: AbortSignal[] = [];
  for (let i = 0; i < N; i++) {
    kept.push(new AbortController().signal);
  }
  console.log(`control: ${N} signals kept: rss ${rssMiB()} MiB (${kept.length})`);
}
longLived.abort();
console.log(`after aborting the source: rss ${rssMiB()} MiB`);
