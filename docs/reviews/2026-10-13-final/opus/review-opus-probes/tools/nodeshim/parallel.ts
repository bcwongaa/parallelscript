// Sequential stand-ins for pts:parallel and .par, for running ParallelScript programs under Node.
const view = (xs: any[]) => ({
  map: (f: any) => xs.map((x: any) => f(x)), filter: (f: any) => xs.filter((x: any) => f(x)),
  forEach: (f: any) => xs.forEach((x: any) => f(x)), reduce: (f: any, init: any) => xs.reduce((a: any, b: any) => f(a, b), init),
  count: (f: any) => xs.filter((x: any) => f(x)).length, sum: () => xs.reduce((a: number, b: number) => a + b, 0),
  min: () => Math.min(...xs), max: () => Math.max(...xs), some: (f: any) => xs.some((x: any) => f(x)),
  every: (f: any) => xs.every((x: any) => f(x)), find: (f: any) => xs.find((x: any) => f(x)),
  flatMap: (f: any) => xs.flatMap((x: any) => f(x)), sort: (c?: any) => xs.sort(c),
  sortBy: (k: any) => xs.sort((a: any, b: any) => (k(a) < k(b) ? -1 : k(a) > k(b) ? 1 : 0)), toArray: () => [...xs],
});
for (const C of [Array, Float64Array, Int32Array, Uint8Array, Uint32Array] as any[]) {
  Object.defineProperty(C.prototype, "par", { get() { return view(C === Array ? this : Array.from(this)); } });
}
export function range(a: number, b: number): number[] { return Array.from({ length: Math.max(0, b - a) }, (_, i) => a + i); }
export function join<A, B>(a: () => A, b: () => B): [A, B] { return [a(), b()]; }
