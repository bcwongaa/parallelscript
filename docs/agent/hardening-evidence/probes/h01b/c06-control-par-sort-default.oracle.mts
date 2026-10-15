// Sequential Node oracle for c06-control-par-sort-default.pts: `xs.par.sort()` is Array's `xs.sort()`.
const small: (number | undefined | null)[] = [3, undefined, 1, null, 20];
console.log(small.sort());
const values: (number | undefined | null)[] = [];
for (let i = 0; i < 6000; i++) {
    values.push(i % 10 === 0 ? undefined : i === 7 ? null : (i * 7919) % 6000);
}
values.sort();
console.log("first", values.slice(0, 5));
console.log("last", values.slice(values.length - 3));
console.log("null at", values.indexOf(null));
