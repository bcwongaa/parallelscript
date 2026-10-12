// The ParallelScript extension: ParallelScript's language server (`pts lsp --stdio`) for
// .pts files. The grammar in syntaxes/ highlights; everything else is the server's,
// including the text of the library files its definitions lead to (ptslib:///), which
// the client asks it for through workspace/textDocumentContent (D-169).
import * as fs from "node:fs";
import * as os from "node:os";
import * as path from "node:path";
import * as vscode from "vscode";
import { LanguageClient, type LanguageClientOptions, type ServerOptions, TransportKind } from "vscode-languageclient/node";

const LANGUAGE_ID = "parallelscript";
const SERVER_PATH_SETTING = "parallelscript.server.path";

let client: LanguageClient | undefined;
// One channel for every client, so restarts don't add channels.
let outputChannel: vscode.LogOutputChannel;

export async function activate(context: vscode.ExtensionContext): Promise<void> {
    outputChannel = vscode.window.createOutputChannel("ParallelScript", { log: true });
    context.subscriptions.push(
        outputChannel,
        vscode.commands.registerCommand("parallelscript.restartServer", restartServer),
        vscode.workspace.onDidChangeConfiguration(event => {
            if (event.affectsConfiguration(SERVER_PATH_SETTING)) {
                void restartServer();
            }
        }),
    );
    await startServer();
}

export async function deactivate(): Promise<void> {
    await stopServer();
}

async function restartServer(): Promise<void> {
    await stopServer();
    await startServer();
}

async function startServer(): Promise<void> {
    const command = resolveServerPath(vscode.workspace.getConfiguration().get<string>(SERVER_PATH_SETTING, "pts"));
    if (!isExecutable(command)) {
        void vscode.window.showErrorMessage(
            `ParallelScript: no language server, since ${command} isn't an executable. Set ${SERVER_PATH_SETTING} to the pts binary.`,
        );
        return;
    }
    client = new LanguageClient(LANGUAGE_ID, "ParallelScript", serverOptions(command), clientOptions());
    // The client shows a server that fails to start itself, with its output.
    await client.start().catch(() => undefined);
}

async function stopServer(): Promise<void> {
    const running = client;
    client = undefined;
    if (running?.needsStop()) {
        await running.stop();
    }
}

/**
 * The server's command from the setting: `~` is the home directory, a relative path with a
 * directory is taken from the first workspace folder, and a bare name is looked up on PATH.
 */
function resolveServerPath(setting: string): string {
    const expanded = setting === "~" || setting.startsWith("~/") ? path.join(os.homedir(), setting.slice(1)) : setting;
    const folder = vscode.workspace.workspaceFolders?.[0];
    if (path.isAbsolute(expanded) || isBareName(expanded) || folder === undefined) {
        return expanded;
    }
    return path.join(folder.uri.fsPath, expanded);
}

function isBareName(command: string): boolean {
    return !command.includes("/") && !command.includes(path.sep);
}

/** Whether command is an executable file: a path, or a bare name found on PATH. */
function isExecutable(command: string): boolean {
    const candidates = isBareName(command)
        ? (process.env.PATH ?? "").split(path.delimiter).filter(dir => dir !== "").map(dir => path.join(dir, command))
        : [command];
    return candidates.some(file => {
        try {
            fs.accessSync(file, fs.constants.X_OK);
            return fs.statSync(file).isFile();
        }
        catch {
            return false;
        }
    });
}

function serverOptions(command: string): ServerOptions {
    // The client adds `--stdio` for the stdio transport.
    const executable = { command, args: ["lsp"], transport: TransportKind.stdio };
    return { run: executable, debug: executable };
}

function clientOptions(): LanguageClientOptions {
    return {
        documentSelector: [
            { scheme: "file", language: LANGUAGE_ID },
            { scheme: "untitled", language: LANGUAGE_ID },
        ],
        // The server's diagnostics are pulled, as typescript-go's are.
        diagnosticPullOptions: { onChange: true, onSave: true },
        outputChannel,
    };
}
