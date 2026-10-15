// Sequential Node oracle for c05-control-par-flatmap-union.pts: `xs.par.flatMap(f)` is Array's `xs.flatMap(f)`.
const nested: (number | number[])[] = [1, [2, 3], 4];
console.log(nested.flatMap((x) => x));
const ones: number[] = [1, 2];
console.log(ones.flatMap((x) => [[x]]));
