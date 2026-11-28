import { spawnSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const scratch = mkdtempSync(join(tmpdir(), "dsh-work-go-test-"));
const packages = process.argv.slice(2);

// Go parses -exec without a shell or escape sequences.
function goArgument(value) {
  if (!value.includes("'")) return `'${value}'`;
  if (!value.includes('"')) return `"${value}"`;
  throw new Error(`Path cannot be passed to go test -exec: ${value}`);
}

function run(command, args) {
  const result = spawnSync(command, args, { cwd: root, stdio: "inherit" });
  if (result.error) throw result.error;
  return result.status ?? 1;
}

try {
  process.exitCode = run(process.execPath, [
    join(root, "scripts/build-workeripc-mac.mjs"), "--output-dir", scratch,
  ]);
  if (process.exitCode === 0) {
    const adapter = [
      process.execPath, join(root, "scripts/with-workeripc-mac.mjs"),
      "--asset", join(scratch, "workeripc-mac.node"),
    ].map(goArgument).join(" ");
    process.exitCode = run("go", [
      "test", "-exec", adapter,
      ...(packages.length > 0 ? packages : [".", "./cmd/...", "./internal/..."]),
    ]);
  }
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
