const trace: string[] = [];
function mark(n: number): number { trace.push(String(n)); return n; }
const source = { get a(): number { trace.push("get"); return 3; }, b: undefined as number | undefined };
const { a, b = mark(a + 1) } = source;
console.log(a, b, trace.join(","));
let x = 0, y = 0;
[x, y = mark(7)] = [mark(2)];
console.log(x, y, trace.join(","));
let index = 0;
const values = [10, 20];
[values[index++], values[index++]] = [30, 40];
console.log(index, values);
function defaults([first = mark(5), second = first + 1]: number[] = []): number { return first + second; }
console.log(defaults(), defaults([8]));
