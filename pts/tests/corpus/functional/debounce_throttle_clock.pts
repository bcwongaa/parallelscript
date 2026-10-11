// Debounce and throttle simulated with a fake clock that runs scheduled callbacks.

class FakeClock {
  private time = 0;
  private seq = 0;
  private queue: Array<{ at: number; id: number; fn: () => void }> = [];

  get now(): number {
    return this.time;
  }

  schedule(delay: number, fn: () => void): number {
    const id = ++this.seq;
    this.queue.push({ at: this.time + delay, id, fn });
    this.queue.sort((a, b) => a.at - b.at || a.id - b.id);
    return id;
  }

  cancel(id: number): boolean {
    const i = this.queue.findIndex((t) => t.id === id);
    if (i < 0) return false;
    this.queue.splice(i, 1);
    return true;
  }

  advanceTo(t: number): void {
    while (this.queue.length > 0 && this.queue[0].at <= t) {
      const task = this.queue.shift()!;
      this.time = task.at;
      task.fn();
    }
    this.time = t;
  }

  get pending(): number {
    return this.queue.length;
  }
}

function debounce<A extends unknown[]>(clock: FakeClock, wait: number, fn: (...args: A) => void) {
  let timer: number | null = null;
  const debounced = (...args: A) => {
    if (timer !== null) clock.cancel(timer);
    timer = clock.schedule(wait, () => {
      timer = null;
      fn(...args);
    });
  };
  const flushPending = () => timer !== null;
  return { call: debounced, isPending: flushPending };
}

function throttle<A extends unknown[]>(clock: FakeClock, interval: number, fn: (...args: A) => void): (...args: A) => void {
  let last = -Infinity;
  let trailing: A | null = null;
  let timer: number | null = null;
  return (...args: A) => {
    const elapsed = clock.now - last;
    if (elapsed >= interval) {
      last = clock.now;
      fn(...args);
    } else {
      trailing = args;
      if (timer === null) {
        timer = clock.schedule(interval - elapsed, () => {
          timer = null;
          last = clock.now;
          if (trailing) {
            const t = trailing;
            trailing = null;
            fn(...t);
          }
        });
      }
    }
  };
}

const clock = new FakeClock();
const searches: string[] = [];
const search = debounce(clock, 300, (q: string) => searches.push(`t=${clock.now} search(${JSON.stringify(q)})`));

const keystrokes: Array<[number, string]> = [
  [0, "p"], [80, "pa"], [150, "par"], [210, "para"], [700, "paral"], [760, "parall"], [1200, "parallel"],
];
for (const [t, q] of keystrokes) {
  clock.advanceTo(t);
  search.call(q);
  console.log(`t=${t} typed ${q.padEnd(9)} pending=${search.isPending()} queue=${clock.pending}`);
}
clock.advanceTo(2000);
console.log(searches);

const clock2 = new FakeClock();
const renders: Array<{ t: number; y: number }> = [];
const onScroll = throttle(clock2, 100, (y: number) => renders.push({ t: clock2.now, y }));
for (let t = 0; t <= 450; t += 30) {
  clock2.advanceTo(t);
  onScroll(t * 2);
}
clock2.advanceTo(1000);
console.log("scroll events: 16, renders:", renders.length);
console.log(renders);

// Autosave: debounce saves but force one every 1000ms of continuous edits
const clock3 = new FakeClock();
const saves: number[] = [];
let dirtySince: number | null = null;
const save = () => {
  saves.push(clock3.now);
  dirtySince = null;
};
const autosave = debounce(clock3, 250, save);
const edit = () => {
  dirtySince ??= clock3.now;
  if (clock3.now - dirtySince >= 1000) save();
  else autosave.call();
};
for (let t = 0; t < 2600; t += 200) {
  clock3.advanceTo(t);
  if (t < 1500 || t > 2000) edit();
}
clock3.advanceTo(4000);
console.log("saves at:", saves, "pending:", clock3.pending);
