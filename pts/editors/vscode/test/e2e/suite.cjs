// The extension in a real VS Code (run.mjs starts it): a .pts file gets the
// parallelscript language, the server's diagnostics with ParallelScript's own, a
// definition in the library that opens with the text the server supplies, hovers,
// semantic tokens that leave the parallel keyword to the grammar, and a restart.
const assert = require("node:assert/strict");
const { writeFileSync } = require("node:fs");
const { join } = require("node:path");
const vscode = require("vscode");

const PROGRAM = `import { range } from "pts:parallel";

const big: u8 = 300;
const total = await parallel range(0, 10).par.sum();
console.log(big, total);
`;

/** The first non-empty list read returns, polling for up to a minute. */
async function waitForSome(what, read) {
    const deadline = Date.now() + 60_000;
    for (;;) {
        const values = await read();
        if (values?.length > 0) {
            return values;
        }
        if (Date.now() > deadline) {
            throw new Error(`timed out waiting for ${what}`);
        }
        await new Promise(resolve => setTimeout(resolve, 250));
    }
}

async function run() {
    const file = join(process.env.PTS_E2E_WORKSPACE, "main.pts");
    writeFileSync(file, PROGRAM);
    const document = await vscode.workspace.openTextDocument(vscode.Uri.file(file));
    await vscode.window.showTextDocument(document);
    assert.equal(document.languageId, "parallelscript");

    const diagnostics = await waitForSome("ParallelScript's diagnostics", () =>
        vscode.languages.getDiagnostics(document.uri).filter(d => d.source === "pts"));
    assert.deepEqual(diagnostics.map(d => [String(d.code), d.range.start.line, d.range.start.character]), [["PTS2002", 2, 16]]);

    const rangeCall = new vscode.Position(3, PROGRAM.split("\n")[3].indexOf("range"));
    const definitions = await waitForSome("a definition of range", () =>
        vscode.commands.executeCommand("vscode.executeDefinitionProvider", document.uri, rangeCall));
    const library = definitions.map(d => d.uri ?? d.targetUri).find(uri => uri.scheme === "ptslib");
    assert.ok(library, `no definition in the library: ${definitions.map(d => String(d.uri ?? d.targetUri))}`);
    const libraryDocument = await vscode.workspace.openTextDocument(library);
    assert.match(libraryDocument.getText(), /export function range\(/);
    assert.equal(libraryDocument.languageId, "parallelscript");

    const hovers = await waitForSome("a hover on big", () =>
        vscode.commands.executeCommand("vscode.executeHoverProvider", document.uri, new vscode.Position(2, 7)));
    const hoverText = hovers.flatMap(h => h.contents).map(c => c.value ?? String(c)).join("\n");
    assert.match(hoverText, /const big: u8/);

    // The keyword of `parallel e` has no semantic token, which would show it as a function.
    const keyword = new vscode.Position(3, PROGRAM.split("\n")[3].indexOf("parallel"));
    const tokens = await vscode.commands.executeCommand("vscode.provideDocumentSemanticTokens", document.uri);
    assert.ok(tokens?.data.length > 0, "no semantic tokens");
    assert.ok(!tokenStarts(tokens.data).some(p => p.isEqual(keyword)), "parallel has a semantic token");
    assert.ok(tokenStarts(tokens.data).some(p => p.isEqual(rangeCall)), "its operand has none");

    await vscode.commands.executeCommand("parallelscript.restartServer");
    await waitForSome("a hover after a restart", () =>
        vscode.commands.executeCommand("vscode.executeHoverProvider", document.uri, new vscode.Position(2, 7)));
}

/** Where each semantic token starts, from LSP's relative encoding. */
function tokenStarts(data) {
    const starts = [];
    let line = 0;
    let character = 0;
    for (let i = 0; i + 4 < data.length; i += 5) {
        character = data[i] === 0 ? character + data[i + 1] : data[i + 1];
        line += data[i];
        starts.push(new vscode.Position(line, character));
    }
    return starts;
}

module.exports = { run };
