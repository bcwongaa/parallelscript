// Fan-out of CPU-heavy tasks, the twin of main.pts without `parallel`: the same 64 tasks
// of uneven sizes (each a sum of 1 to 8 million terms with a square root and a division)
// run one after another.
const TASKS = 64;
const MILLION = 1_000_000;

function series(task: number): number {
    const terms = ((task % 8) + 1) * MILLION;
    let sum = 0;
    for (let k = 1; k <= terms; k++) {
        sum += Math.sqrt(k + task) / (k * k + 1);
    }
    return sum;
}

const sums = Array.from({ length: TASKS }, (_, task) => series(task));
console.log(sums.reduce((a, b) => a + b, 0), Math.max(...sums));
