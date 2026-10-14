function counter(start: number): [() => number, () => number] {
  let value = start;
  return [() => ++value, () => value];
}
const [next, read] = counter(2);
console.log(next(), read(), next(), read());
const callbacks: (() => number)[] = [];
for (let i = 0; i < 5; i++) callbacks.push(() => i);
console.log(callbacks.map((f) => f()));
function thrown(): number {
  let n = 1;
  const change = () => { n = 8; };
  try { change(); throw "x"; } catch { return n; }
}
console.log(thrown());
