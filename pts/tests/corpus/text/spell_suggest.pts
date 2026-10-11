// A spell checker that suggests corrections using Levenshtein and Damerau distances,
// ranked by distance and word frequency.
const DICTIONARY_TEXT = `the of and to in is you that it he was for on are as with his they at be this have from
or one had by word but not what all were we when your can said there use an each which she do how their if will
up other about out many then them these so some her would make like him into time has look two more write go see
number no way could people my than first water been call who oil its now find long down day did get come made may
part receive believe separate definitely necessary occurrence accommodate government environment
language compiler parallel script garbage collector thread memory native binary`;

const freq = new Map<string, number>();
DICTIONARY_TEXT.split("\n").join(" ").split(" ").filter(Boolean).forEach((w, i) => freq.set(w, 1000 - i));

function levenshtein(a: string, b: string): number {
  if (a === b) return 0;
  if (a.length === 0) return b.length;
  if (b.length === 0) return a.length;
  let prev = Array.from({ length: b.length + 1 }, (_, j) => j);
  for (let i = 1; i <= a.length; i++) {
    const cur = [i];
    for (let j = 1; j <= b.length; j++) {
      const cost = a[i - 1] === b[j - 1] ? 0 : 1;
      cur[j] = Math.min(prev[j] + 1, cur[j - 1] + 1, prev[j - 1] + cost);
    }
    prev = cur;
  }
  return prev[b.length];
}

function damerau(a: string, b: string): number {
  const d: number[][] = [];
  for (let i = 0; i <= a.length; i++) {
    d.push([]);
    for (let j = 0; j <= b.length; j++) {
      if (i === 0) d[i].push(j);
      else if (j === 0) d[i].push(i);
      else {
        const cost = a[i - 1] === b[j - 1] ? 0 : 1;
        let v = Math.min(d[i - 1][j] + 1, d[i][j - 1] + 1, d[i - 1][j - 1] + cost);
        if (i > 1 && j > 1 && a[i - 1] === b[j - 2] && a[i - 2] === b[j - 1]) v = Math.min(v, d[i - 2][j - 2] + 1);
        d[i].push(v);
      }
    }
  }
  return d[a.length][b.length];
}

interface Suggestion {
  word: string;
  distance: number;
  frequency: number;
}

class SpellChecker {
  constructor(private readonly words: Map<string, number>, private readonly maxDistance = 2) {}

  check(word: string): boolean {
    return this.words.has(word.toLowerCase());
  }

  suggest(word: string, limit = 3, metric: (a: string, b: string) => number = damerau): Suggestion[] {
    const w = word.toLowerCase();
    const out: Suggestion[] = [];
    for (const [cand, frequency] of this.words) {
      if (Math.abs(cand.length - w.length) > this.maxDistance) continue;
      const distance = metric(w, cand);
      if (distance <= this.maxDistance) out.push({ word: cand, distance, frequency });
    }
    out.sort((x, y) => x.distance - y.distance || y.frequency - x.frequency || (x.word < y.word ? -1 : 1));
    return out.slice(0, limit);
  }

  correctSentence(sentence: string): { text: string; fixes: [string, string][] } {
    const fixes: [string, string][] = [];
    const words = sentence.split(" ").map((tok) => {
      if (tok === "" || this.check(tok)) return tok;
      const best = this.suggest(tok, 1)[0];
      if (!best) return tok;
      fixes.push([tok, best.word]);
      return best.word;
    });
    return { text: words.join(" "), fixes };
  }
}

const checker = new SpellChecker(freq);
console.log("dictionary size:", freq.size);

const pairs: [string, string][] = [["kitten", "sitting"], ["flaw", "lawn"], ["ca", "abc"], ["recieve", "receive"], ["", "abc"], ["same", "same"]];
for (const [a, b] of pairs) console.log(`${a.padEnd(8)} ${b.padEnd(8)} lev=${levenshtein(a, b)} dam=${damerau(a, b)}`);

for (const w of ["teh", "recieve", "seperate", "definately", "langauge", "complier", "garbge", "thred", "xyzzy", "The"]) {
  const known = checker.check(w);
  const s = known ? [] : checker.suggest(w);
  console.log(`${w.padEnd(11)} ${known ? "ok" : "-> " + (s.map((x) => `${x.word}(${x.distance})`).join(", ") || "no suggestions")}`);
}

console.log(checker.suggest("teh", 5, levenshtein));
console.log(checker.suggest("teh", 5));
const result = checker.correctSentence("teh compiler wil recieve a paralel scrpt adn emit native bianry");
console.log(result.text);
console.log(result.fixes);
console.log(new Set(result.fixes.map(([, to]) => to.length)));
