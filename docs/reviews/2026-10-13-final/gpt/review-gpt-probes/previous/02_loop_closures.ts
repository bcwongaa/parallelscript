const callbacks: (() => number)[] = [];
for (let i = 0; i < 4; i++) {
    callbacks.push(() => i);
}
console.log(callbacks.map(fn => fn()).join(","));
const later: Promise<number>[] = [];
for (let i = 0; i < 4; i++) {
    later.push(Promise.resolve().then(() => i));
}
console.log((await Promise.all(later)).join(","));
