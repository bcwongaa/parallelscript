// Sum and reduce over 100 million numbers, the twin of main.pts without `.par`: a
// Float64Array filled by a loop, then a summing loop, `reduce` with an addition and the
// largest value, two rounds.

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
    let largest = -Infinity;
    for (let i = 0; i < COUNT; i++) {
        total += values[i];
        largest = Math.max(largest, values[i]);
    }
    sum += total;
    reduced += values.reduce((a, b) => a + b, 0);
    max = Math.max(max, largest);
}
console.log(sum, reduced, max);
