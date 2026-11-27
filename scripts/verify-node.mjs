import { readFileSync } from "node:fs";
import { execFileSync, spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(readFileSync(join(root, "toolchain.lock.json"), "utf8"));
const npmCommand = process.platform === "win32" ? "cmd.exe" : "npm";
const npmArgs = process.platform === "win32" ? ["/d", "/c", "npm", "--version"] : ["--version"];
const npmVersion = execFileSync(npmCommand, npmArgs, { encoding: "utf8" }).trim();
const minimumNodeVersion = manifest.node.minimumVersion;
const minimumNpmMajor = manifest.packageManager.minimumMajor;
const currentNpmMajor = Number.parseInt(npmVersion.split(".")[0], 10);

const versionParts = (value) => value.split(".").map((part) => Number.parseInt(part, 10));
const versionAtLeast = (actual, minimum) => {
  const actualParts = versionParts(actual);
  const minimumParts = versionParts(minimum);
  for (let index = 0; index < Math.max(actualParts.length, minimumParts.length); index += 1) {
    const actualPart = actualParts[index] ?? 0;
    const minimumPart = minimumParts[index] ?? 0;
    if (actualPart !== minimumPart) return actualPart > minimumPart;
  }
  return true;
};

if (!versionAtLeast(process.versions.node, minimumNodeVersion)) {
  throw new Error(`Node mismatch: expected >=${minimumNodeVersion}, got ${process.version}`);
}
// frontend/package-lock.json and `npm ci` pin dependencies; the npm release
// itself only needs to read that lockfile.
if (!(currentNpmMajor >= minimumNpmMajor)) {
  throw new Error(`npm mismatch: expected >=${minimumNpmMajor}, got ${npmVersion}`);
}

if (process.argv.includes("--desktop-toolchain")) {
  const go = execFileSync("go", ["version"], { encoding: "utf8" }).trim();
  if (go.split(/\s+/)[2] !== `go${manifest.go.version}`) {
    throw new Error(`Go mismatch: expected go${manifest.go.version}, got ${go}`);
  }
  const result = spawnSync("wails3", ["version"], { encoding: "utf8" });
  if (result.error) throw result.error;
  const wails = (result.stdout + "\n" + result.stderr).trim();
  if (result.status !== 0) throw new Error(`wails3 version failed: ${wails}`);
  if (wails.match(/v\d+\.\d+\.\d+(?:-[\w.-]+)?/)?.[0] !== manifest.wails.cli) {
    throw new Error(`Wails mismatch: expected ${manifest.wails.cli}, got ${wails}`);
  }
  const dsh = manifest.dsh;
  const fixture = JSON.parse(readFileSync(join(root, "tools/dsh/package.json"), "utf8"));
  const lock = JSON.parse(readFileSync(join(root, "tools/dsh/package-lock.json"), "utf8"));
  if (fixture.dependencies[dsh.package] !== dsh.version || lock.packages[`node_modules/${dsh.package}`]?.version !== dsh.version) {
    throw new Error(`DSH development fixture mismatch: expected ${dsh.package}@${dsh.version}`);
  }
  console.log(`toolchain: go=${manifest.go.version} wails=${manifest.wails.cli} dsh=${dsh.version}`);
}
console.log(`node=${process.version} npm=${npmVersion}`);
