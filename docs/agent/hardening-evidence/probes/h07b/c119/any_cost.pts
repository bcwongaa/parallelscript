// H07b C119 measurement: AbortSignal.any([shared]) n times, then aborting shared with n followers.
// N from the environment (default 10000). Prints ms for following and for the abort.
const N = Number(process.env.N ?? "10000");
const shared = new AbortController();
const followers: AbortSignal[] = [];
const t0 = performance.now();
for (let i = 0; i < N; i++) {
  followers.push(AbortSignal.any([shared.signal]));
}
const t1 = performance.now();
shared.abort("stop");
const t2 = performance.now();
let aborted = 0;
for (const follower of followers) {
  if (follower.aborted && follower.reason === "stop") {
    aborted++;
  }
}
console.log(`n ${N}: follow ${(t1 - t0).toFixed(0)} ms, abort ${(t2 - t1).toFixed(0)} ms, aborted ${aborted}`);
