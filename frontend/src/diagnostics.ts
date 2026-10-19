import {Browser, Clipboard} from "@wailsio/runtime";
import {DiagnosticsService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import type {Problem} from "../bindings/github.com/local/dsh-work/internal/diagnostics/models";
import {issueURL} from "./diagnostics-model";

// latestProblem is the newest problem, or undefined when there is none or
// the list cannot be read; reports then carry only the environment.
export async function latestProblem(): Promise<Problem | undefined> {
  try { return (await DiagnosticsService.GetProblems())?.[0]; } catch { return undefined; }
}

export async function copyReport(problem?: Problem): Promise<void> {
  await Clipboard.SetText(await DiagnosticsService.GetReport(problem?.id ?? ""));
}

// reportOnGitHub copies the report first; the issue form gets only a title.
export async function reportOnGitHub(problem?: Problem): Promise<void> {
  await copyReport(problem);
  await Browser.OpenURL(issueURL(problem));
}
