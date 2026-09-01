import { execFileSync } from "node:child_process";

function git(args) {
  return execFileSync("git", args, { encoding: "utf8" });
}

const trackedResearch = git(["ls-files", "--cached", "--", ".research"]).trim();
if (trackedResearch !== "") {
  throw new Error(`Private research material is tracked:\n${trackedResearch}`);
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

console.log("public-content: no tracked .research files; git whitespace checks passed");
