import { execFileSync, spawnSync } from "node:child_process";
import { resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const write = process.argv.includes("--write");
const search = ["--files", "-0", "-g", "*.go", "-g", "!tools/**", "-g", "!.tmp-wails-*/"];
// The generated daemon client intentionally uses the generator's formatting.
if (!write) search.push("-g", "!internal/desktopclient/**");

const result = spawnSync("rg", search, { cwd: root, encoding: "utf8" });
if (result.error) throw result.error;
if (result.status !== 0 && result.status !== 1) {
  throw new Error(`Could not list Go files: ${result.stderr}`);
}

const files = result.stdout.split("\0").filter(Boolean);
const unformatted = [];
// Keep each command line below Windows' process argument limit.
for (let index = 0; index < files.length; index += 50) {
  const output = execFileSync("gofmt", [write ? "-w" : "-l", ...files.slice(index, index + 50)], {
    cwd: root, encoding: "utf8",
  }).trim();
  if (output) unformatted.push(output);
}

if (unformatted.length > 0) {
  console.error("Unformatted Go files\n" + unformatted.join("\n"));
  process.exitCode = 1;
}
