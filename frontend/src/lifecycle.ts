export type LifecycleState = "Starting" | "Ready" | "Stopping" | "Stopped" | "Failed";
export type LifecyclePhase = "idle" | "configuration" | "runtime" | "worker" | "readiness" | "workspace" | "stopping" | "failed";

export interface LifecycleFailure {
  code: string;
  summary: string;
  retryable: boolean;
  effectOccurred: boolean;
  correlationId: string;
  detail?: string;
}

export interface LifecycleStatus {
  state: LifecycleState;
  phase: LifecyclePhase;
  generationId?: string;
  workspaceUrl?: string;
  error?: LifecycleFailure;
  canRetry: boolean;
  canCancel: boolean;
  correlationId?: string;
}

export interface LifecycleViewModel {
  label: string;
  message: string;
  detail: string;
  tone: "progress" | "ready" | "failed" | "quiet";
  showCancel: boolean;
  showRetry: boolean;
  workspace: string;
}

const phaseLabels: Record<LifecyclePhase, string> = {
  idle: "Idle",
  configuration: "Preparing",
  runtime: "Checking DSH",
  worker: "Starting DSH",
  readiness: "Checking readiness",
  workspace: "Workspace ready",
  stopping: "Stopping",
  failed: "Needs attention"
};

export function viewModel(status: LifecycleStatus): LifecycleViewModel {
  const failure = status.error;
  if (status.state === "Failed") {
    return {
      label: "Failed",
      message: failure?.summary ?? "Work could not start the local workspace.",
      detail: failure?.detail ?? failure?.code ?? "UNKNOWN_FAILURE",
      tone: "failed",
      showCancel: false,
      showRetry: status.canRetry,
      workspace: "No trusted workspace origin was accepted."
    };
  }
  if (status.state === "Ready") {
    return {
      label: "Ready",
      message: "The trusted DSH workspace is ready.",
      detail: "Handing the window over to the loopback workspace.",
      tone: "ready",
      showCancel: true,
      showRetry: false,
      workspace: status.workspaceUrl ?? "Loopback origin accepted."
    };
  }
  if (status.state === "Stopping") {
    return {
      label: "Stopping",
      message: "Cleaning up the managed DSH process.",
      detail: "Work will verify that the process boundary is empty.",
      tone: "progress",
      showCancel: false,
      showRetry: false,
      workspace: "Workspace is closing."
    };
  }
  if (status.state === "Stopped") {
    return {
      label: "Stopped",
      message: "The local workspace is stopped.",
      detail: "Start again when you are ready.",
      tone: "quiet",
      showCancel: false,
      showRetry: true,
      workspace: "No active workspace."
    };
  }
  return {
    label: phaseLabels[status.phase],
    message: "Work is preparing the managed DSH process.",
    detail: "Only a validated loopback origin can become the workspace.",
    tone: "progress",
    showCancel: status.canCancel,
    showRetry: false,
    workspace: "Waiting for a trusted loopback origin."
  };
}
