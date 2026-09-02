import {readFileSync} from "node:fs";
import {resolve} from "node:path";

const root = resolve(import.meta.dirname, "..");
const taskfile = readFileSync(resolve(root, "Taskfile.yml"), "utf8");
const config = readFileSync(resolve(root, "build", "config.yml"), "utf8");
const failures = [];

if (!/\n\s+frontend:dev:\s*\n/.test(taskfile)) {
  failures.push("Taskfile.yml must define frontend:dev");
}
if (!/\n\s+frontend:install:\s*[\s\S]*?\n\s+sources:\s*\n[\s\S]*?package\.json[\s\S]*?package-lock\.json[\s\S]*?\n\s+generates:\s*\n[\s\S]*?node_modules/.test(taskfile)) {
  failures.push("frontend:install must be incremental so dev and build tasks do not run npm ci concurrently");
}
if (!/npm run dev -- --port \{\{\.WAILS_VITE_PORT \| default 9245\}\} --strictPort/.test(taskfile)) {
  failures.push("frontend:dev must pass WAILS_VITE_PORT and --strictPort to Vite");
}
if (!/\n\s+- cmd: wails3 task frontend:dev\s*\n\s+type: background\s*/.test(config)) {
  failures.push("build/config.yml must start frontend:dev as a background task");
}
const installTask = config.indexOf("- cmd: wails3 task frontend:install");
const devTask = config.indexOf("- cmd: wails3 task frontend:dev");
if (installTask < 0 || devTask < 0 || installTask > devTask || !/\n\s+- cmd: wails3 task frontend:install\s*\n\s+type: blocking\s*/.test(config)) {
  failures.push("build/config.yml must install frontend dependencies before starting the background dev server");
}

if (failures.length > 0) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else {
  console.log("wails-dev-config: valid");
}
