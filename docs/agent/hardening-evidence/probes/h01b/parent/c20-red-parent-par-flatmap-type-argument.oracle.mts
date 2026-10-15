// Sequential Node equivalent of c20-red-parent-par-flatmap-type-argument.pts (.par.flatMap becomes flatMap).
const values: number[] = [1, 2];
console.log(values.flatMap<number | number[]>((x) => (x === 1 ? x : [[x]])));
console.log(values.flatMap<number | number[]>((x) => (x === 1 ? x : [[x]])));
