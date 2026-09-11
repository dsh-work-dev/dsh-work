import { execFileSync } from "node:child_process";

function git(args) {
  return execFileSync("git", args, { encoding: "utf8" });
}

for (const directory of [".research", ".evolve"]) {
  const tracked = git(["ls-files", "--cached", "--", directory]).trim();
  if (tracked !== "") throw new Error(`Private working material is tracked in ${directory}:\n${tracked}`);
}

for (const args of [
  ["diff", "--check"],
  ["diff", "--cached", "--check"],
]) {
  try {
    git(args);
  } catch (error) {
    throw new Error(`Git whitespace check failed for ${args.join(" ")}: ${error}`);
  }
}

console.log("public-content: no tracked Research/Evolve files; git whitespace checks passed");
