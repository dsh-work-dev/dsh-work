import type {LifecyclePhase} from "../lifecycle";

export type PublicStep = "node" | "dsh" | "profile" | "service";
export const publicSteps: readonly PublicStep[] = ["node", "dsh", "profile", "service"];
export type StepState = "done" | "active" | "failed" | "pending";

export interface StepInput {
  ready: boolean;          // status.state === "Ready"
  starting: boolean;       // status.state === "Starting"
  phase: LifecyclePhase | string; // status.phase while Starting, otherwise the last starting phase
  acquisition?: "node" | "dsh";
  failed: boolean;         // status.state === "Failed" && !busy
  errorCode: string;
  missing: boolean;        // a snapshot is loaded and Node or DSH is not installed
  nodeInstalled: boolean;
  active: boolean;         // Starting, Stopping or Ready
  busy: boolean;
}

/** Index into publicSteps of the step in progress or at fault; -1 when none applies. */
export function currentStep(input: StepInput): number {
  const {phase, acquisition, errorCode: code} = input;
  let current = acquisition === "node" ? 0 : acquisition === "dsh" ? 1 : phase === "node" || phase === "configuration" ? 0 : phase === "runtime" ? 1 : phase === "profile" ? 2 : 3;
  if (input.failed) current = code.includes("NODE") ? 0 : code.includes("PROFILE") ? 2 : code.includes("RUNTIME") || code.includes("VERSION") ? 1 : code.startsWith("DSH_") || code.includes("PROCESS") ? 3 : -1;
  if (input.missing && !input.active && !input.busy) current = !input.nodeInstalled ? 0 : 1;
  return current;
}

export function stepStates(input: StepInput, current: number): StepState[] {
  return publicSteps.map((_, index) => {
    const done = input.ready || index < current;
    const running = index === current && (input.busy || input.starting);
    return done ? "done" : index === current && input.failed ? "failed" : running ? "active" : "pending";
  });
}

/** (step index + fraction within the step) / 4; Ready is 1. */
export function overallProgress(current: number, within: number | null, ready: boolean): number {
  if (ready) return 1;
  const index = Math.max(0, Math.min(publicSteps.length - 1, current));
  const fraction = within === null || !Number.isFinite(within) ? 0 : Math.min(1, Math.max(0, within));
  return (index + fraction) / publicSteps.length;
}

export interface AttentionInput { failed: boolean; stopped: boolean; error: boolean; connectionFailed: boolean; missing: boolean; active: boolean; busy: boolean }
/** The recovery panel is shown when startup needs the user. Downloads in progress keep it closed. */
export function startupAttention(i: AttentionInput): boolean {
  if (i.busy) return false;
  return i.failed || i.stopped || i.error || i.connectionFailed || (i.missing && !i.active);
}
