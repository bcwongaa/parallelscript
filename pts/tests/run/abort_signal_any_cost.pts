// Following one signal costs O(1), as Node's AbortSignal.any does (C119, H07b1): 400,000
// followers of one long-lived signal, then aborting it. Node takes about 1 s; with followers
// that cost O(n) each (a listener list scanned for duplicates at every add) it takes minutes,
// far past the test's 30 s limit (100,000 took 130 s on the base on a loaded host).
const FOLLOWERS = 400_000;
const shared = new AbortController();
const followers: AbortSignal[] = [];
for (let i = 0; i < FOLLOWERS; i++) {
  followers.push(AbortSignal.any([shared.signal]));
}
// A follower of a follower follows the first one's source, at the same cost.
const inner = followers[0];
const outer: AbortSignal[] = [];
for (let i = 0; i < FOLLOWERS / 4; i++) {
  outer.push(AbortSignal.any([inner]));
}
shared.abort("stop");
let aborted = 0;
for (const follower of [...followers, ...outer]) {
  if (follower.aborted && follower.reason === "stop") {
    aborted++;
  }
}
console.log(`followers ${followers.length + outer.length}, aborted ${aborted}`);

// expect:
// followers 500000, aborted 500000
