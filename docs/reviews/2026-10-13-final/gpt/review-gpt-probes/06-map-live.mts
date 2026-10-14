const m = new Map<string, number>([["a", 1], ["b", 2], ["c", 3]]);
const iterator = m.entries();
console.log(iterator.next().value);
m.delete("b"); m.set("d", 4); m.set("a", 5);
console.log(iterator.next().value, iterator.next().value, iterator.next().done);
const s = new Set([1, 2, 3]);
const seen: number[] = [];
for (const n of s) { seen.push(n); if (n === 1) { s.delete(2); s.add(4); } }
console.log(seen, m.size);
