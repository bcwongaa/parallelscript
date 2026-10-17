// Sequential Node twin of pts/tests/run/abort_signal_shared_parallel.pts (H07): range(0, n).par.map(f)
// as Array.from({ length: n }, (_, i) => f(i)).
const shared = new AbortController();
const followers = Array.from({ length: 10_000 }, () => {
  try {
    return AbortSignal.any([shared.signal]);
  } catch (error) {
    return null;
  }
});
shared.abort("stop");
let made = 0;
let aborted = 0;
for (const follower of followers) {
  if (follower !== null) {
    made++;
    if (follower.aborted && follower.reason === "stop") {
      aborted++;
    }
  }
}
console.log(`followers ${made}, aborted ${aborted}`);
