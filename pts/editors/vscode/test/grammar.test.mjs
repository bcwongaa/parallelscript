// The ParallelScript grammar, tokenized by vscode-textmate and Oniguruma as VS Code does.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import oniguruma from "vscode-oniguruma";
import textmate from "vscode-textmate";

const GRAMMAR = fileURLToPath(new URL("../syntaxes/ParallelScript.tmLanguage.json", import.meta.url));
const KEYWORD = "keyword.control.parallel.pts";

const require = createRequire(import.meta.url);
await oniguruma.loadWASM(readFileSync(require.resolve("vscode-oniguruma/release/onig.wasm")).buffer);
const registry = new textmate.Registry({
    onigLib: Promise.resolve({
        createOnigScanner: patterns => new oniguruma.OnigScanner(patterns),
        createOnigString: text => new oniguruma.OnigString(text),
    }),
    loadGrammar: async scopeName =>
        scopeName === "source.pts" ? textmate.parseRawGrammar(readFileSync(GRAMMAR, "utf8"), GRAMMAR) : null,
});
const grammar = await registry.loadGrammar("source.pts");

const SAMPLES = JSON.parse(readFileSync(new URL("parallel_samples.json", import.meta.url), "utf8")).samples;

/** The tokens of source, each its offset, text and scopes. */
function tokenize(source) {
    const tokens = [];
    let state = textmate.INITIAL;
    let lineStart = 0;
    for (const line of source.split("\n")) {
        const result = grammar.tokenizeLine(line, state);
        for (const token of result.tokens) {
            tokens.push({ offset: lineStart + token.startIndex, text: line.slice(token.startIndex, token.endIndex), scopes: token.scopes });
        }
        state = result.ruleStack;
        lineStart += line.length + 1;
    }
    return tokens;
}

/** For each `parallel` in source, in order, whether the grammar scopes it as the keyword. */
function parallelKeywords(source) {
    const tokens = tokenize(source);
    return [...source.matchAll(/(?<![\w$])parallel(?![\w$])/g)].map(match =>
        tokens.some(token => token.offset === match.index && token.text === "parallel" && token.scopes.includes(KEYWORD))
    );
}

function scopesOfFirst(source, text) {
    const token = tokenize(source).find(token => token.text.trim() === text);
    assert.ok(token, `no token ${text} in ${source}`);
    return token.scopes;
}

test("parallel is the keyword exactly where the parser takes it for one", () => {
    for (const { program, keywords } of SAMPLES) {
        assert.deepEqual(parallelKeywords(program), keywords, program);
    }
});

test("for keeps its scope after the parallel keyword", () => {
    assert.ok(scopesOfFirst("parallel for (const x of xs) {}", "for").includes("keyword.control.loop.pts"));
});

test("parallel called with parentheses is a function call", () => {
    assert.ok(scopesOfFirst("const p = parallel(fib(30));", "parallel").includes("entity.name.function.pts"));
});

test("no scope is TypeScript's", () => {
    const program = [
        "import { range } from \"pts:parallel\";",
        "class Counter<T> { #n: u8 = 0; get n(): number { return this.#n; } }",
        "export async function main(xs: number[]): Promise<void> {",
        "  const doubled = await parallel xs.map(x => x * 2);",
        "  console.log(`${doubled.length} /re+/g`, /re+/g.test(\"ree\"));",
        "}",
    ].join("\n");
    for (const token of tokenize(program)) {
        for (const scope of token.scopes) {
            assert.doesNotMatch(scope, /\.ts$/, `${token.text}: ${scope}`);
        }
    }
});
