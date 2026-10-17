// Sequential Node oracle for c07-shared-signal-any-par.pts: `range(0, n).par.map(f)` is
// `Array.from({ length: n }, (_, i) => f(i))`; everything else is unchanged.
const shared = new AbortController();
const followers = Array.from({ length: 20000 }, (_, i) => {
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
console.log(`followers ${made}, aborted ${aborted}, listeners lost ${made - aborted}, construction failures ${followers.length - made}`);
