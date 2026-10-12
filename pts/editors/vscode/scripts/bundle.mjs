// Bundles src/extension.ts with the npm packages it uses into dist/extension.js, and
// writes dist/ThirdPartyNotices.txt: ThirdPartyNotices.txt followed by the license of
// each package in the bundle.
import { build } from "esbuild";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const ROOT = fileURLToPath(new URL("..", import.meta.url));
const LICENSE_FILE = /^licen[cs]e(\.(md|txt))?$/i;

const result = await build({
    absWorkingDir: ROOT,
    entryPoints: ["src/extension.ts"],
    bundle: true,
    platform: "node",
    format: "cjs",
    target: "node20",
    external: ["vscode"],
    outfile: "dist/extension.js",
    minify: true,
    metafile: true,
    logLevel: "warning",
});

/** The directories, under node_modules, of the packages the bundle took files from. */
function bundledPackages(metafile) {
    const packages = new Set();
    for (const input of Object.keys(metafile.inputs)) {
        const match = input.match(/node_modules\/((?:@[^/]+\/)?[^/]+)\//);
        if (match) {
            packages.add(match[1]);
        }
    }
    return [...packages].sort();
}

function packageNotice(name) {
    const directory = `${ROOT}node_modules/${name}/`;
    const { version, license } = JSON.parse(readFileSync(`${directory}package.json`, "utf8"));
    const licenseFile = readdirSync(directory).find(file => LICENSE_FILE.test(file));
    if (licenseFile === undefined) {
        throw new Error(`${name} has no license file to ship`);
    }
    return `${name} ${version} (${license})\n\n${readFileSync(directory + licenseFile, "utf8").trim()}\n`;
}

const notices = [readFileSync(`${ROOT}ThirdPartyNotices.txt`, "utf8").trim() + "\n"];
for (const name of bundledPackages(result.metafile)) {
    notices.push(packageNotice(name));
}
writeFileSync(`${ROOT}dist/ThirdPartyNotices.txt`, notices.join(`\n${"-".repeat(72)}\n\n`));
