class Counter {
  value = 1;
  get total(): number { return this.value; }
  set total(next: number) { this.value = next; }
}
const counter = new Counter();
function* accessor(): Generator<number, number, number> {
  counter.total += yield 0;
  return counter.total;
}
const a = accessor();
a.next();
counter.value = 2;
console.log("accessor", a.next(10).value);

const dynamic: any = { total: 1 };
function* anyTarget(): Generator<number, number, number> {
  dynamic.total += yield 0;
  return dynamic.total;
}
const d = anyTarget();
d.next();
dynamic.total = 2;
console.log("any", d.next(10).value);

const methods = { m: (n: number): string => `old ${n}` };
function* lookup(): Generator<number, string, number> {
  return methods.m(yield 0);
}
const m = lookup();
m.next();
methods.m = (n: number): string => `new ${n}`;
console.log("method", m.next(10).value);
