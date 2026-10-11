// Longest common subsequence used to produce a line diff and a character-level similarity score.

type DiffOp = { kind: "keep" | "add" | "del"; text: string };

function lcsTable<T>(a: readonly T[], b: readonly T[], eq: (x: T, y: T) => boolean = (x, y) => x === y): number[][] {
  const t: number[][] = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0));
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      t[i][j] = eq(a[i], b[j]) ? t[i + 1][j + 1] + 1 : Math.max(t[i + 1][j], t[i][j + 1]);
    }
  }
  return t;
}

function lcsString(a: string, b: string): string {
  const A = [...a];
  const B = [...b];
  const t = lcsTable(A, B);
  let i = 0;
  let j = 0;
  let out = "";
  while (i < A.length && j < B.length) {
    if (A[i] === B[j]) {
      out += A[i];
      i++;
      j++;
    } else if (t[i + 1][j] >= t[i][j + 1]) i++;
    else j++;
  }
  return out;
}

function diff(before: string[], after: string[]): DiffOp[] {
  const t = lcsTable(before, after);
  const ops: DiffOp[] = [];
  let i = 0;
  let j = 0;
  while (i < before.length || j < after.length) {
    if (i < before.length && j < after.length && before[i] === after[j]) {
      ops.push({ kind: "keep", text: before[i] });
      i++;
      j++;
    } else if (j < after.length && (i === before.length || t[i][j + 1] >= t[i + 1][j])) {
      ops.push({ kind: "add", text: after[j++] });
    } else {
      ops.push({ kind: "del", text: before[i++] });
    }
  }
  return ops;
}

const v1 = `function greet(name) {
  const msg = "Hello, " + name;
  console.log(msg);
  return msg;
}
greet("world");`.split("\n");

const v2 = `function greet(name: string): string {
  const msg = \`Hello, \${name}!\`;
  console.log(msg);
  return msg;
}
greet("world");
greet("again");`.split("\n");

const ops = diff(v1, v2);
const sigil = { keep: " ", add: "+", del: "-" } as const;
for (const op of ops) console.log(`${sigil[op.kind]} ${op.text}`);
const counts = ops.reduce(
  (acc, op) => ({ ...acc, [op.kind]: acc[op.kind] + 1 }),
  { keep: 0, add: 0, del: 0 },
);
console.log("stats:", counts);

const pairs: [string, string][] = [
  ["AGGTAB", "GXTXAYB"],
  ["kitten", "sitting"],
  ["parallel", "script"],
  ["", "abc"],
  ["typescript", "javascript"],
];
for (const [a, b] of pairs) {
  const l = lcsString(a, b);
  const sim = a.length + b.length === 0 ? 1 : (2 * l.length) / (a.length + b.length);
  console.log("%s vs %s -> %o (similarity %s)", JSON.stringify(a), JSON.stringify(b), l, sim.toFixed(3));
}

const wordsA = "the cat sat on the mat today".split(" ");
const wordsB = "a cat sat quietly on a mat".split(" ");
const wt = lcsTable(wordsA, wordsB);
console.log("common words:", wt[0][0], "of", Math.max(wordsA.length, wordsB.length));
const ci = lcsTable([..."HeLLo"], [..."hello"], (x, y) => x.toLowerCase() === y.toLowerCase());
console.log("case-insensitive LCS length:", ci[0][0]);
console.log(diff(["a", "b", "c"], ["a", "c", "d"]));
