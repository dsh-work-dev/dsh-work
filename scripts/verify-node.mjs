import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";

const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const manifest = JSON.parse(readFileSync(join(root, "toolchain.lock.json"), "utf8"));
const npmCommand = process.platform === "win32" ? "cmd.exe" : "npm";
const npmArgs = process.platform === "win32" ? ["/d", "/c", "npm", "--version"] : ["--version"];
const npmVersion = execFileSync(npmCommand, npmArgs, { encoding: "utf8" }).trim();
const expectedNode = `v${manifest.node.version}`;
const expectedNpm = manifest.packageManager.version;

if (process.version !== expectedNode) {
  throw new Error(`Node mismatch: expected ${expectedNode}, got ${process.version}`);
}
if (npmVersion !== expectedNpm) {
  throw new Error(`npm mismatch: expected ${expectedNpm}, got ${npmVersion}`);
}

console.log(`node=${process.version} npm=${npmVersion}`);
