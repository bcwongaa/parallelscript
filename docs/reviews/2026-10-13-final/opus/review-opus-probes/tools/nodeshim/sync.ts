export class AtomicInt { v: number; constructor(v = 0) { this.v = v; } load() { return this.v; } store(x: number) { this.v = x; }
  add(x: number) { const o = this.v; this.v += x; return o; } compareExchange(e: number, n: number) { const o = this.v; if (o === e) this.v = n; return o; } }
export class Mutex<T> { constructor(public value: T) {} withLock<R>(f: (v: T) => R): R { return f(this.value); }
  async lock() { const self = this; return { get value() { return self.value; }, unlock() {} }; } }
export class Once { done = false; run(f: () => void) { if (!this.done) { f(); this.done = true; } } }
export class Channel<T> { q: T[] = []; waiters: ((v: T | undefined) => void)[] = []; closed = false;
  constructor(public cap = Infinity) {}
  async send(v: T) { if (this.closed) throw new Error("Send on a closed channel"); const w = this.waiters.shift(); if (w) w(v); else this.q.push(v);
    while (this.q.length > this.cap) await new Promise((r) => setTimeout(r, 0)); }
  async recv(): Promise<T | undefined> { if (this.q.length) return this.q.shift(); if (this.closed) return undefined; return new Promise((r) => this.waiters.push(r)); }
  close() { this.closed = true; for (const w of this.waiters.splice(0)) w(undefined); }
  async *[Symbol.asyncIterator]() { for (;;) { const v = await this.recv(); if (v === undefined && this.closed && !this.q.length) return; yield v as T; } } }
