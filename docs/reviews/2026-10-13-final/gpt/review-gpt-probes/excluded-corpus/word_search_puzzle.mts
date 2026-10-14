// A word search puzzle: deterministic grid generation with a seeded PRNG, placement in
// eight directions with overlaps, filling, solving, and rendering the solution overlay.
function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

type Direction = readonly [dr: number, dc: number, label: string];
const DIRECTIONS: Direction[] = [
  [0, 1, "E"], [1, 0, "S"], [1, 1, "SE"], [-1, 1, "NE"], [0, -1, "W"], [-1, 0, "N"], [-1, -1, "NW"], [1, -1, "SW"],
];

interface Placement { word: string; row: number; col: number; dir: string }

class Puzzle {
  readonly grid: string[][];
  readonly placements: Placement[] = [];
  readonly skipped: string[] = [];
  private readonly rand: () => number;

  constructor(readonly size: number, seed: number) {
    this.grid = Array.from({ length: size }, () => new Array<string>(size).fill("."));
    this.rand = mulberry32(seed);
  }

  private int(n: number): number {
    return Math.floor(this.rand() * n);
  }

  private fits(word: string, r: number, c: number, [dr, dc]: Direction): boolean {
    for (let i = 0; i < word.length; i++) {
      const rr = r + dr * i, cc = c + dc * i;
      if (rr < 0 || cc < 0 || rr >= this.size || cc >= this.size) return false;
      const cell = this.grid[rr][cc];
      if (cell !== "." && cell !== word[i]) return false;
    }
    return true;
  }

  place(word: string): boolean {
    const w = word.toUpperCase();
    for (let attempt = 0; attempt < 200; attempt++) {
      const dir = DIRECTIONS[this.int(DIRECTIONS.length)];
      const r = this.int(this.size), c = this.int(this.size);
      if (!this.fits(w, r, c, dir)) continue;
      for (let i = 0; i < w.length; i++) this.grid[r + dir[0] * i][c + dir[1] * i] = w[i];
      this.placements.push({ word: w, row: r, col: c, dir: dir[2] });
      return true;
    }
    this.skipped.push(w);
    return false;
  }

  fill(): void {
    const letters = "ABCDEFGHIJKLMNOPRSTUVWY";
    for (const row of this.grid) for (let c = 0; c < row.length; c++) if (row[c] === ".") row[c] = letters[this.int(letters.length)];
  }

  find(word: string): Placement | null {
    const w = word.toUpperCase();
    for (let r = 0; r < this.size; r++) {
      for (let c = 0; c < this.size; c++) {
        if (this.grid[r][c] !== w[0]) continue;
        for (const dir of DIRECTIONS) {
          let ok = true;
          for (let i = 1; i < w.length && ok; i++) {
            const rr = r + dir[0] * i, cc = c + dir[1] * i;
            ok = rr >= 0 && cc >= 0 && rr < this.size && cc < this.size && this.grid[rr][cc] === w[i];
          }
          if (ok) return { word: w, row: r, col: c, dir: dir[2] };
        }
      }
    }
    return null;
  }

  render(highlight: Placement[] = []): string {
    const marked = new Set<string>();
    for (const p of highlight) {
      const dir = DIRECTIONS.find((d) => d[2] === p.dir);
      if (!dir) continue;
      for (let i = 0; i < p.word.length; i++) marked.add(`${p.row + dir[0] * i},${p.col + dir[1] * i}`);
    }
    const header = "   " + Array.from({ length: this.size }, (_, i) => String(i % 10)).join(" ");
    const rows = this.grid.map((row, r) => `${String(r).padStart(2)} ` + row.map((ch, c) => (highlight.length ? (marked.has(`${r},${c}`) ? ch : "·") : ch)).join(" "));
    return [header, ...rows].join("\n");
  }
}

const words = ["typescript", "compiler", "parallel", "thread", "garbage", "native", "binary", "closure", "generic", "union", "tuple", "enum", "heap", "stack"];
const puzzle = new Puzzle(12, 2024);
const sortedWords = [...words].sort((a, b) => b.length - a.length);
for (const w of sortedWords) puzzle.place(w);
console.log(`placed ${puzzle.placements.length}/${words.length}`, puzzle.skipped.length ? `skipped: ${puzzle.skipped.join(", ")}` : "");
console.log(puzzle.render(puzzle.placements));
puzzle.fill();
console.log(puzzle.render());
const found = words.map((w) => puzzle.find(w));
for (const f of found) if (f) console.log(`  ${f.word.padEnd(11)} at (${f.row},${f.col}) going ${f.dir}`);
console.log("missing:", words.filter((_, i) => found[i] === null));
const dirCounts = puzzle.placements.reduce<Record<string, number>>((acc, p) => ((acc[p.dir] = (acc[p.dir] ?? 0) + 1), acc), {});
console.log(dirCounts);
const letterTotals = new Map<string, number>();
for (const row of puzzle.grid) for (const ch of row) letterTotals.set(ch, (letterTotals.get(ch) ?? 0) + 1);
console.log([...letterTotals].sort((a, b) => b[1] - a[1]).slice(0, 6));
const rng = mulberry32(42);
console.log(Array.from({ length: 5 }, () => rng().toFixed(6)));
console.log(puzzle.placements[0]);
