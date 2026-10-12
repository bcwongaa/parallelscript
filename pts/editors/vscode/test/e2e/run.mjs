// Runs the extension in a real VS Code, headless (Electron's ozone headless platform, no
// display), on a scratch workspace, with the suite in suite.cjs. The server is
// PTS_E2E_SERVER, by default the repository's pts-bin/pts (`make build` first).
import { runTests } from "@vscode/test-electron";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const VSCODE_VERSION = "1.141.0";
const EXTENSION = fileURLToPath(new URL("../..", import.meta.url));
const server = process.env.PTS_E2E_SERVER ?? fileURLToPath(new URL("../../../../../pts-bin/pts", import.meta.url));

const scratch = mkdtempSync(join(tmpdir(), "pts-vscode-e2e-"));
const workspace = join(scratch, "workspace");
mkdirSync(join(workspace, ".vscode"), { recursive: true });
writeFileSync(join(workspace, ".vscode", "settings.json"), JSON.stringify({ "parallelscript.server.path": server }));

const env = { ...process.env };
delete env.DISPLAY;
delete env.WAYLAND_DISPLAY;
try {
    await runTests({
        version: VSCODE_VERSION,
        cachePath: process.env.PTS_VSCODE_CACHE ?? join(homedir(), ".cache", "pts-vscode-test"),
        extensionDevelopmentPath: EXTENSION,
        extensionTestsPath: fileURLToPath(new URL("suite.cjs", import.meta.url)),
        extensionTestsEnv: { PTS_E2E_WORKSPACE: workspace },
        launchArgs: [
            workspace,
            "--disable-extensions",
            "--disable-gpu",
            "--ozone-platform=headless",
            "--user-data-dir", join(scratch, "user-data"),
        ],
        env,
    });
}
finally {
    rmSync(scratch, { recursive: true, force: true });
}
