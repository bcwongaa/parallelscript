// Sum and reduce over 100 million numbers, the twin of main.pts without `.par`: a
// Float64Array filled by a loop, then a summing loop, `reduce` with an addition and a
// loop for the largest value, two rounds. Each is a pass of its own over the array, as
// `.par.sum()`, `.par.reduce` and `.par.max()` are.

const COUNT = 100_000_000;
const ROUNDS = 2;

const values = new Float64Array(COUNT);
for (let i = 0; i < COUNT; i++) {
    values[i] = (i & 1023) * 0.25;
}
let sum = 0;
let reduced = 0;
let max = 0;
for (let round = 0; round < ROUNDS; round++) {
    let total = 0;
    for (let i = 0; i < COUNT; i++) {
        total += values[i];
    }
    sum += total;
    reduced += values.reduce((a, b) => a + b, 0);
    let largest = -Infinity;
    for (let i = 0; i < COUNT; i++) {
        largest = Math.max(largest, values[i]);
    }
    max = Math.max(max, largest);
}
console.log(sum, reduced, max);
