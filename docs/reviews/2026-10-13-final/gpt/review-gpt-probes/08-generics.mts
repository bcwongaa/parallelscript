class Box<T> { value: T; constructor(value: T) { this.value = value; } map<U>(f: (x: T) => U): Box<U> { return new Box(f(this.value)); } }
function apply<T, U>(items: T[], f: (item: T) => U): U[] { return items.map(f); }
function make<T>(x: T): Box<T> { return new Box(x); }
const maker = make;
console.log(apply([1, 2, 3], (n) => maker(n).map((v) => String(v)).value));
interface Transform { run<T>(value: T): T[] }
const transformer: Transform = { run<T>(value: T): T[] { return [value, value]; } };
console.log(transformer.run(3), transformer.run("a"));
function nested<T>(value: T): () => T { return () => value; }
console.log(nested({ name: "retained" })().name);
