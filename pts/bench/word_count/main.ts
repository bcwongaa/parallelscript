// Word count over many files, the twin of main.pts without `.par`: 64 generated text
// files (made once, in a directory under /tmp) read and counted file by file into maps,
// which are then merged; prints the distinct and total words and the five most common.
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";

const DIR = "/tmp/pts-bench-word-count-1";
const FILES = 64;
const WORDS_PER_FILE = 100_000;
const VOCABULARY = 5_000;
const SYLLABLES = ["ka", "lo", "mi", "ne", "ru", "sa", "to", "vi", "ze", "po", "an", "el"];

// Park and Miller's generator: exact in doubles, so every implementation makes the same files.
let seed = 42;
function next(): number {
    seed = (seed * 48271) % 2147483647;
    return seed;
}

function word(index: number): string {
    let text = "";
    for (let n = index + 1; n > 0; n = Math.floor(n / SYLLABLES.length)) {
        text += SYLLABLES[n % SYLLABLES.length];
    }
    return text;
}

function generate(): void {
    mkdirSync(DIR, { recursive: true });
    const words: string[] = [];
    for (let i = 0; i < VOCABULARY; i++) words.push(word(i));
    for (let f = 0; f < FILES; f++) {
        const lines: string[] = [];
        let line: string[] = [];
        for (let w = 0; w < WORDS_PER_FILE; w++) {
            const r = next() / 2147483647;
            line.push(words[Math.floor(r * r * r * VOCABULARY)]);
            if (line.length === 12) {
                lines.push(line.join(" ") + ".");
                line = [];
            }
        }
        lines.push(line.join(" "));
        writeFileSync(`${DIR}/${f}.txt`, lines.join("\n") + "\n");
    }
    writeFileSync(`${DIR}/done`, "");
}

function isLetter(code: number): boolean {
    return code >= 97 && code <= 122;
}

function countWords(path: string): Map<string, number> {
    const text = readFileSync(path, "utf8");
    const counts = new Map<string, number>();
    let start = -1;
    for (let i = 0; i <= text.length; i++) {
        const letter = i < text.length && isLetter(text.charCodeAt(i));
        if (letter && start < 0) {
            start = i;
        } else if (!letter && start >= 0) {
            const found = text.slice(start, i);
            counts.set(found, (counts.get(found) ?? 0) + 1);
            start = -1;
        }
    }
    return counts;
}

if (!existsSync(`${DIR}/done`)) generate();
const paths: string[] = [];
for (let f = 0; f < FILES; f++) paths.push(`${DIR}/${f}.txt`);
const perFile = paths.map(countWords);
const totals = new Map<string, number>();
for (const counts of perFile) {
    for (const [found, n] of counts) totals.set(found, (totals.get(found) ?? 0) + n);
}
let all = 0;
for (const n of totals.values()) all += n;
const top = [...totals].sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1)).slice(0, 5);
console.log(totals.size, all, top.map(([found, n]) => `${found}:${n}`).join(" "));
