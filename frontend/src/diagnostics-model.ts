export type ProblemGroup = "runtime" | "start" | "plugin" | "profile" | "storage" | "update" | "other";
export type ProblemAction = "runtimes" | "restart" | "plugins" | "profiles" | "data-directories" | "check-updates";

export const issueNewURL = "https://github.com/dsh-work-dev/dsh-work/issues/new";
const issueTitleLimit = 120;

const runtimeCodes = new Set([
  "DSH_RUNTIME_NOT_FOUND",
  "DSH_VERSION_CHECK_FAILED",
  "DSH_UNSUPPORTED_VERSION",
  "RUNTIME_PROFILE_INCOMPATIBLE"
]);
const startCodes = new Set([
  "DSH_START_FAILED",
  "DSH_EARLY_EXIT",
  "DSH_READINESS_TIMEOUT",
  "DSH_INVALID_READINESS",
  "PROCESS_START_FAILED"
]);
const storageCodes = new Set([
  "RECOVERY_POINT_SAVE_FAILED",
  "MANAGER_STATE_INVALID"
]);
const updateCodes = new Set([
  "UPDATE_DOWNLOAD_FAILED",
  "UPDATE_INSTALL_FAILED"
]);

export function problemGroup(code: string): ProblemGroup {
  if (runtimeCodes.has(code) || code.startsWith("RUNTIME_INSTALL_")) return "runtime";
  if (startCodes.has(code)) return "start";
  if (code.startsWith("PLUGIN_")) return "plugin";
  if (code.startsWith("PROFILE_") || code === "WORKSPACE_INVALID") return "profile";
  if (storageCodes.has(code) || code.startsWith("SETTINGS_")) return "storage";
  if (updateCodes.has(code)) return "update";
  return "other";
}

export function problemActions(group: ProblemGroup): ProblemAction[] {
  switch (group) {
    case "runtime": return ["runtimes"];
    case "start": return ["restart", "plugins"];
    case "plugin": return ["plugins"];
    case "profile": return ["profiles"];
    case "storage": return ["data-directories"];
    case "update": return ["check-updates"];
    case "other": return [];
  }
}

export function issueURL(problem?: {code: string; message: string}): string {
  const url = issueNewURL + "?template=bug_report.yml";
  if (!problem) return url;
  const title = Array.from(`${problem.code}: ${problem.message}`).slice(0, issueTitleLimit).join("");
  return url + "&title=" + encodeURIComponent(title);
}