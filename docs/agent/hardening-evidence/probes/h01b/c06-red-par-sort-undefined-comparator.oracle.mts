// Sequential Node oracle for c06-red-par-sort-undefined-comparator.pts: `values.par.sort(f)` is Array's
// `values.sort(f)` (pts:parallel documents `.par.sort` as Array's sort) and the AtomicInt is a plain counter.
const values: (number | undefined)[] = [];
for (let i = 0; i < 6000; i++) {
    values.push(i % 10 === 0 ? undefined : (i * 7919) % 6000);
}
let undefinedArguments = 0;
values.sort((a, b) => {
    if (a === undefined || b === undefined) {
        undefinedArguments += 1;
    }
    return (a ?? -1) - (b ?? -1);
});
const defined = values.filter((value) => value !== undefined);
console.log("first", defined.slice(0, 5));
console.log("last", values.slice(values.length - 3));
console.log("undefined count", values.length - defined.length);
console.log("defined ascending:", defined.every((value, i) => i === 0 || (defined[i - 1] ?? 0) < (value ?? 0)));
console.log("comparator saw undefined:", undefinedArguments > 0);
